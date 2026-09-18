package tracker

import (
	"github.com/mgdavisxvs/Ocelot/ml"
)

// NewSwarmHealthPredictor returns a SwarmHealthInterface backed by the ml
// package's heuristic predictor.
func NewSwarmHealthPredictor() SwarmHealthInterface {
	return ml.NewSwarmHealthPredictor()
}

// ClientDetector checks peer_id bytes and User-Agent strings for known-bad
// client patterns before the peer is admitted to any swarm.
type ClientDetector interface {
	DetectClientAnomaly(clientID, userAgent string) (bool, string)
}

// NewClientDetector returns a production ClientDetector backed by the ml
// package's pattern database.
func NewClientDetector() ClientDetector {
	return ml.NewClientAnomalyDetector()
}

// BehaviorDetector is the seam between the tracker and the ML anomaly layer.
// Keeping it as an interface lets tests inject a stub without importing ml.
type BehaviorDetector interface {
	// Detect returns (true, reason) when the peer exhibits anomalous behaviour.
	// upSpeed and downSpeed are bytes-per-second deltas for the current interval.
	// torrentSize is the total torrent size in bytes; 0 disables the
	// impossible_download check.
	Detect(peer *Peer, upSpeed, downSpeed, torrentSize int64) (bool, string)
}

// mlAnomalyAdapter wraps ml.AnomalyDetector to satisfy BehaviorDetector.
type mlAnomalyAdapter struct {
	d *ml.AnomalyDetector
}

// NewAnomalyDetector returns a production BehaviorDetector backed by the
// ml.AnomalyDetector with its default thresholds.
func NewAnomalyDetector() BehaviorDetector {
	return &mlAnomalyAdapter{d: ml.NewAnomalyDetector()}
}

// NewAnomalyDetectorPair returns a BehaviorDetector and the underlying
// *ml.AnomalyDetector so callers can call SetThresholds without the interface
// hiding the method.
func NewAnomalyDetectorPair() (BehaviorDetector, *ml.AnomalyDetector) {
	d := ml.NewAnomalyDetector()
	return &mlAnomalyAdapter{d: d}, d
}

func (a *mlAnomalyAdapter) Detect(peer *Peer, upSpeed, downSpeed, torrentSize int64) (bool, string) {
	b := &ml.PeerBehavior{
		Uploaded:        peer.Uploaded,
		Downloaded:      peer.Downloaded,
		UploadSpeed:     float64(upSpeed),
		AnnounceCount:   int(peer.Announces),
		FirstSeen:       peer.FirstAnnounced,
		PortHistory:     []uint16{peer.Port},
		TorrentSize:     torrentSize,
		ConnectionTimes: peer.ConnectionTimes,
	}
	return a.d.DetectAnomaly(b)
}
