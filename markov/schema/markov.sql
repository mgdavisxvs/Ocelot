-- Ocelot Markov Analytics Schema
-- Run against the same Gazelle MySQL database.
-- All tables are prefixed markov_ to avoid collisions.

CREATE TABLE IF NOT EXISTS markov_chain_counts (
    chain_name  VARCHAR(32)  NOT NULL,
    from_state  TINYINT      NOT NULL,
    to_state    TINYINT      NOT NULL,
    count       DOUBLE       NOT NULL DEFAULT 0,
    PRIMARY KEY (chain_name, from_state, to_state)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
  COMMENT='Markov transition count matrices for peer, user, and torrent chains';

CREATE TABLE IF NOT EXISTS markov_peer_states (
    torrent_id  INT     NOT NULL,
    uid         INT     NOT NULL,
    state       TINYINT NOT NULL COMMENT '0=LEECHING 1=SEEDING 2=DORMANT 3=SNATCHED 4=DEAD',
    observed_at BIGINT  NOT NULL COMMENT 'Unix timestamp',
    PRIMARY KEY (torrent_id, uid),
    KEY idx_torrent (torrent_id),
    KEY idx_uid (uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
  COMMENT='Last observed peer lifecycle state per (torrent, user) pair';

CREATE TABLE IF NOT EXISTS markov_torrent_states (
    torrent_id  INT     NOT NULL PRIMARY KEY,
    state       TINYINT NOT NULL COMMENT '0=THRIVING 1=HEALTHY 2=AT_RISK 3=DYING 4=DEAD',
    observed_at BIGINT  NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
  COMMENT='Last observed swarm health state per torrent';

CREATE TABLE IF NOT EXISTS markov_user_states (
    uid         INT     NOT NULL PRIMARY KEY,
    state       TINYINT NOT NULL COMMENT '0=HEALTHY 1=WARNING 2=PROBATION 3=BANNED 4=FREELEECH',
    path_json   TEXT             COMMENT 'JSON array of recent state IDs for fraud detection',
    observed_at BIGINT  NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
  COMMENT='Last observed ratio state + path history per user';

CREATE TABLE IF NOT EXISTS markov_predictions (
    torrent_id           INT     NOT NULL PRIMARY KEY,
    health_state         TINYINT NOT NULL,
    pi_json              TEXT    NOT NULL COMMENT 'JSON: current state distribution π',
    pi_24h_json          TEXT    NOT NULL COMMENT 'JSON: forecast π at +24h',
    pi_72h_json          TEXT    NOT NULL COMMENT 'JSON: forecast π at +72h',
    dead_prob_24h        DOUBLE  NOT NULL COMMENT 'P(TorrentDead) at +24h',
    dead_prob_72h        DOUBLE  NOT NULL COMMENT 'P(TorrentDead) at +72h',
    expected_dead_hours  DOUBLE  NOT NULL COMMENT 'E[hours until TorrentDead from current state]',
    entropy              DOUBLE  NOT NULL COMMENT 'Shannon entropy H(π) of current distribution',
    recommended_interval INT     NOT NULL COMMENT 'Adaptive announce interval in seconds',
    updated_at           BIGINT  NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
  COMMENT='Markov-derived health predictions per torrent. Gazelle reads recommended_interval.';

CREATE TABLE IF NOT EXISTS markov_user_anomaly (
    uid                  INT        NOT NULL PRIMARY KEY,
    anomaly_score        DOUBLE     NOT NULL COMMENT 'z-score relative to population NLL mean',
    path_log_likelihood  DOUBLE     NOT NULL COMMENT 'Negative log-likelihood of observed ratio path',
    flagged              TINYINT(1) NOT NULL DEFAULT 0 COMMENT '1 if anomaly_score > threshold',
    updated_at           BIGINT     NOT NULL,
    KEY idx_flagged (flagged)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
  COMMENT='Fraud detection scores per user. Gazelle moderation queue reads flagged=1.';

CREATE TABLE IF NOT EXISTS markov_freeleech_candidates (
    torrent_id    INT        NOT NULL PRIMARY KEY,
    priority_score DOUBLE    NOT NULL COMMENT 'dead_prob_72h * (1 + entropy)',
    dead_prob_72h  DOUBLE    NOT NULL,
    recommended    TINYINT(1) NOT NULL DEFAULT 0,
    updated_at     BIGINT    NOT NULL,
    KEY idx_recommended (recommended),
    KEY idx_priority (priority_score DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
  COMMENT='Torrents recommended for freeleech to attract seeders before swarm death.';
