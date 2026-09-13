package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mgdavisxvs/Ocelot/tracker"
)

func main() {
	fmt.Println("Ocelot BitTorrent Tracker (Go Edition)")

	// ── Configuration ─────────────────────────────────────────────────────────
	configPath := tracker.ParseFlags()

	fc, err := tracker.ParseConfigFile(configPath)
	if err != nil {
		log.Fatalf("Failed to load config %q: %v", configPath, err)
	}
	config := fc.ToTrackerConfig()
	log.Printf("Config loaded from %s", configPath)

	// ── Database ──────────────────────────────────────────────────────────────
	db, err := tracker.NewSQLiteShardManager(fc.DBDir)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()
	log.Printf("SQLite database ready in %s", fc.DBDir)

	// ── In-memory state ───────────────────────────────────────────────────────
	torrents := tracker.NewTorrentList()
	users := tracker.NewUserList()
	whitelist := tracker.NewWhitelist()
	stats := &tracker.Stats{StartTime: time.Now()}

	// ── Load initial state ────────────────────────────────────────────────────
	loader := tracker.NewLoader(db, torrents, users, whitelist)
	if err := loader.LoadAll(); err != nil {
		log.Printf("Warning: initial state load failed: %v", err)
		log.Println("Starting with empty state; add torrents and users via admin API")
	} else {
		log.Printf("State loaded: %d torrents, %d users", torrents.Size(), users.Size())
	}

	// ── Site communication ────────────────────────────────────────────────────
	var siteComm tracker.SiteCommInterface
	if config.GazelleURL != "" {
		siteComm = tracker.NewGazelleSiteComm(config.GazelleURL, config.SitePassword)
		log.Printf("Gazelle callbacks enabled: %s", config.GazelleURL)
	} else {
		siteComm = &tracker.NoOpSiteComm{}
		log.Println("No gazelle_url configured; token expiry callbacks disabled")
	}

	// ── Worker ────────────────────────────────────────────────────────────────
	worker := &tracker.Worker{
		Config:    config,
		DB:        db,
		SiteComm:  siteComm,
		Torrents:  torrents,
		Users:     users,
		Whitelist: whitelist,
		Stats:     stats,
	}

	// ── Redis backend (optional) ──────────────────────────────────────────────
	if config.RedisURL != "" {
		rb, err := tracker.NewRedisBackendFromURL(config.RedisURL)
		if err != nil {
			log.Printf("Warning: Redis unavailable (%v); continuing without it", err)
		} else {
			worker.Redis = rb
			log.Printf("Redis backend connected: %s", config.RedisURL)
		}
	}

	// ── Swarm coordination plane ──────────────────────────────────────────────
	artifacts := tracker.NewArtifactList()
	nodes := tracker.NewNodeRegistry()
	replicas := tracker.NewNodeReplicaMap()
	admission := tracker.NewSwarmAdmissionPolicy()
	controller := tracker.NewSwarmPolicyController(
		artifacts, torrents, nodes, replicas, admission, 60*time.Second,
	)
	if config.AlertWebhookURL != "" {
		controller.SetAlertWebhookURL(config.AlertWebhookURL)
		log.Printf("Flap alert webhook: %s", config.AlertWebhookURL)
	}

	// S-E4: Proximity-aware peer sort; S-E6: heatmap recording.
	worker.PeerSorter = tracker.SortPeersByProximity(nodes)
	worker.Artifacts = artifacts
	worker.Admission = admission

	// ── Background subsystems ─────────────────────────────────────────────────
	reaper := tracker.NewReaper(torrents, config.ScheduleInterval, config.PeersTimeout)
	reaper.Start()
	defer reaper.Stop()

	scheduler := tracker.NewScheduler(db, config.ScheduleInterval)
	scheduler.Start()
	defer scheduler.Stop()

	// ── Signal handlers ───────────────────────────────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGUSR1)

	go func() {
		for sig := range sigCh {
			switch sig {
			case syscall.SIGHUP:
				newFC, err := tracker.ParseConfigFile(configPath)
				if err != nil {
					log.Printf("SIGHUP: failed to reload config: %v", err)
					continue
				}
				newCfg := newFC.ToTrackerConfig()
				// Update fields that can change without a server restart.
				config.SitePassword = newCfg.SitePassword
				config.ReportPassword = newCfg.ReportPassword
				config.NumWantLimit = newCfg.NumWantLimit
				config.AnnounceInterval = newCfg.AnnounceInterval
				config.PeersTimeout = newCfg.PeersTimeout
				log.Println("SIGHUP: configuration reloaded")

			case syscall.SIGUSR1:
				log.Println("SIGUSR1: reloading torrent/user/whitelist state...")
				if err := loader.Reload(); err != nil {
					log.Printf("SIGUSR1: reload failed: %v", err)
				} else {
					log.Printf("SIGUSR1: reload complete — %d torrents, %d users",
						torrents.Size(), users.Size())
				}
			}
		}
	}()

	// ── Servers (M-03: port split :34000/:34001/:34002) ──────────────────────
	server := tracker.NewServer(config, worker)
	controlSrv := tracker.NewControlServer(config, worker)
	controlSrv.AttachControllerDeps(nodes, replicas, admission, controller)
	controlSrv.RegisterAgentRoutes()
	opsSrv, readyFlag := tracker.NewOpsServer(config, worker)
	opsSrv.AttachSwarmDeps(nodes, replicas, artifacts)

	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("Tracker plane listening on %s", config.ListenAddr)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("Tracker server error: %v", err)
		}
	}()
	go func() {
		if err := controlSrv.ListenAndServe(); err != nil {
			log.Printf("Control server error: %v", err)
		}
	}()
	go func() {
		if err := opsSrv.ListenAndServe(); err != nil {
			log.Printf("Ops server error: %v", err)
		}
	}()

	// Swarm policy controller runs until shutdown.
	controllerCtx, stopController := context.WithCancel(context.Background())
	go controller.Run(controllerCtx)
	defer stopController()

	// Drain controller directives — log JOIN_SWARM and RETIRE_REPLICA actions.
	go func() {
		for {
			select {
			case <-controllerCtx.Done():
				return
			case act, ok := <-controller.Directives():
				if !ok {
					return
				}
				log.Printf("swarm directive: %s node=%d hash=%s", act.Directive, act.NodeID, act.InfoHash)
			}
		}
	}()

	// Mark ready after all servers are started.
	readyFlag.SetReady()

	<-shutdownCh
	log.Println("Shutdown signal received — draining connections...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(); err != nil {
		log.Printf("Tracker shutdown error: %v", err)
	}
	if err := controlSrv.Shutdown(ctx); err != nil {
		log.Printf("Control shutdown error: %v", err)
	}
	if err := opsSrv.Shutdown(ctx); err != nil {
		log.Printf("Ops shutdown error: %v", err)
	}
	log.Println("Shutdown complete")
}
