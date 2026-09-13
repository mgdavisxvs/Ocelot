package db

import (
	"context"
	"database/sql"
	"encoding/json"
)

// ModelMetadata holds versioned governance metadata for a named chain.
type ModelMetadata struct {
	ChainName       string
	SchemaVersion   int
	DecayLambda     float64
	SmoothingAlpha  float64
	ObsIntervalSec  int
	MatrixVersion   int
	EffSamples      []float64 // effective sample count per state
	CreatedAt       int64
	UpdatedAt       int64
}

// UpsertModelMetadata writes or updates governance metadata for a chain.
func (d *DB) UpsertModelMetadata(ctx context.Context, m ModelMetadata) error {
	sampJSON := "[]"
	if b, err := json.Marshal(m.EffSamples); err == nil {
		sampJSON = string(b)
	}
	_, err := d.pool.ExecContext(ctx, `
		INSERT INTO markov_model_metadata
			(chain_name, schema_version, decay_lambda, smoothing_alpha,
			 obs_interval_sec, matrix_version, eff_samples_json, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(chain_name) DO UPDATE SET
			schema_version=excluded.schema_version,
			decay_lambda=excluded.decay_lambda,
			smoothing_alpha=excluded.smoothing_alpha,
			obs_interval_sec=excluded.obs_interval_sec,
			matrix_version=excluded.matrix_version,
			eff_samples_json=excluded.eff_samples_json,
			updated_at=excluded.updated_at`,
		m.ChainName, m.SchemaVersion, m.DecayLambda, m.SmoothingAlpha,
		m.ObsIntervalSec, m.MatrixVersion, sampJSON, m.CreatedAt, m.UpdatedAt)
	return err
}

// LoadModelMetadata retrieves governance metadata for all chains.
func (d *DB) LoadModelMetadata(ctx context.Context) ([]ModelMetadata, error) {
	rows, err := d.pool.QueryContext(ctx,
		`SELECT chain_name, schema_version, decay_lambda, smoothing_alpha,
		        obs_interval_sec, matrix_version, eff_samples_json, created_at, updated_at
		 FROM markov_model_metadata`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelMetadata
	for rows.Next() {
		var m ModelMetadata
		var sampJSON sql.NullString
		if err := rows.Scan(&m.ChainName, &m.SchemaVersion, &m.DecayLambda, &m.SmoothingAlpha,
			&m.ObsIntervalSec, &m.MatrixVersion, &sampJSON, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		if sampJSON.Valid && sampJSON.String != "" {
			_ = json.Unmarshal([]byte(sampJSON.String), &m.EffSamples)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RecommendationRecord is a single logged recommendation from the Markov engine.
type RecommendationRecord struct {
	ID                int64
	ChainName         string
	EntityID          int64
	PredictionJSON    string
	Entropy           float64
	EvidenceStrength  float64
	ModelVersion      int
	RecommendedAction string
	PolicyAction      string
	Outcome           string
	ShadowMode        bool
	CreatedAt         int64
	ResolvedAt        int64
}

// InsertRecommendation records a single model recommendation in audit log.
func (d *DB) InsertRecommendation(ctx context.Context, r RecommendationRecord) error {
	shadow := 0
	if r.ShadowMode {
		shadow = 1
	}
	_, err := d.pool.ExecContext(ctx, `
		INSERT INTO markov_recommendation_log
			(chain_name, entity_id, prediction_json, entropy, evidence_strength,
			 model_version, recommended_action, policy_action, outcome, shadow_mode, created_at, resolved_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ChainName, r.EntityID, r.PredictionJSON, r.Entropy, r.EvidenceStrength,
		r.ModelVersion, r.RecommendedAction, r.PolicyAction, r.Outcome, shadow, r.CreatedAt, r.ResolvedAt)
	return err
}

// LoadRecentRecommendations returns the most recent N recommendations.
func (d *DB) LoadRecentRecommendations(ctx context.Context, limit int) ([]RecommendationRecord, error) {
	rows, err := d.pool.QueryContext(ctx, `
		SELECT id, chain_name, entity_id, prediction_json, entropy, evidence_strength,
		       model_version, recommended_action, policy_action, outcome, shadow_mode, created_at, resolved_at
		FROM markov_recommendation_log
		ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecommendationRecord
	for rows.Next() {
		var r RecommendationRecord
		var shadow int
		if err := rows.Scan(&r.ID, &r.ChainName, &r.EntityID, &r.PredictionJSON,
			&r.Entropy, &r.EvidenceStrength, &r.ModelVersion,
			&r.RecommendedAction, &r.PolicyAction, &r.Outcome,
			&shadow, &r.CreatedAt, &r.ResolvedAt); err != nil {
			return nil, err
		}
		r.ShadowMode = shadow != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// ForecastEvalRecord is a single stored forecast for later evaluation.
type ForecastEvalRecord struct {
	ID           int64
	ChainName    string
	EntityID     int64
	HorizonSteps int
	ForecastJSON string // JSON-encoded []float64 distribution
	OutcomeState int    // -1 = not yet evaluated
	BrierScore   float64
	LogLoss      float64
	CreatedAt    int64
	EvaluatedAt  int64
}

// InsertForecastEval stores a forecast for later calibration evaluation.
func (d *DB) InsertForecastEval(ctx context.Context, r ForecastEvalRecord) error {
	_, err := d.pool.ExecContext(ctx, `
		INSERT INTO markov_forecast_evaluation
			(chain_name, entity_id, horizon_steps, forecast_json,
			 outcome_state, brier_score, log_loss, created_at, evaluated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		r.ChainName, r.EntityID, r.HorizonSteps, r.ForecastJSON,
		r.OutcomeState, r.BrierScore, r.LogLoss, r.CreatedAt, r.EvaluatedAt)
	return err
}

// UpdateForecastOutcome records the actual outcome and computes calibration scores.
func (d *DB) UpdateForecastOutcome(ctx context.Context, id int64, outcomeState int, brier, logLoss float64, evaluatedAt int64) error {
	_, err := d.pool.ExecContext(ctx,
		`UPDATE markov_forecast_evaluation
		 SET outcome_state=?, brier_score=?, log_loss=?, evaluated_at=?
		 WHERE id=?`,
		outcomeState, brier, logLoss, evaluatedAt, id)
	return err
}

// LoadPendingForecastEvals returns forecasts not yet evaluated (outcome_state = -1)
// that were created before beforeUnix (so the horizon has elapsed).
func (d *DB) LoadPendingForecastEvals(ctx context.Context, beforeUnix int64, limit int) ([]ForecastEvalRecord, error) {
	rows, err := d.pool.QueryContext(ctx, `
		SELECT id, chain_name, entity_id, horizon_steps, forecast_json,
		       outcome_state, brier_score, log_loss, created_at, evaluated_at
		FROM markov_forecast_evaluation
		WHERE outcome_state=-1 AND created_at<?
		LIMIT ?`, beforeUnix, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ForecastEvalRecord
	for rows.Next() {
		var r ForecastEvalRecord
		if err := rows.Scan(&r.ID, &r.ChainName, &r.EntityID, &r.HorizonSteps,
			&r.ForecastJSON, &r.OutcomeState, &r.BrierScore, &r.LogLoss,
			&r.CreatedAt, &r.EvaluatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CalibrationSummary holds aggregate calibration metrics for a chain/horizon.
type CalibrationSummary struct {
	ChainName    string
	HorizonSteps int
	Count        int
	MeanBrier    float64
	MeanLogLoss  float64
}

// LoadCalibrationSummary returns mean Brier and log-loss per chain/horizon.
func (d *DB) LoadCalibrationSummary(ctx context.Context) ([]CalibrationSummary, error) {
	rows, err := d.pool.QueryContext(ctx, `
		SELECT chain_name, horizon_steps, COUNT(*), AVG(brier_score), AVG(log_loss)
		FROM markov_forecast_evaluation
		WHERE outcome_state >= 0
		GROUP BY chain_name, horizon_steps
		ORDER BY chain_name, horizon_steps`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalibrationSummary
	for rows.Next() {
		var s CalibrationSummary
		if err := rows.Scan(&s.ChainName, &s.HorizonSteps, &s.Count, &s.MeanBrier, &s.MeanLogLoss); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
