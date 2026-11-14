package speech

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	"time"
)

// TranscribeSession finishes the provider/limit/billing snapshot accepted when
// recording began, even if the administrator has since selected another route.
func (s *Service) TranscribeSession(ctx context.Context, user uint, provider, requestID, id, token string, audio domain.Audio) (domain.Transcript, error) {
	s.mu.Lock()
	v := s.sessions[id]
	if v == nil {
		s.mu.Unlock()
		return s.replaySessionTranscription(ctx, user, provider, requestID, id, audio)
	}
	if v.user != user || v.provider.ASRCapability().Streaming || time.Now().After(v.expires) || subtle.ConstantTimeCompare([]byte(token), []byte(v.wsTicket)) != 1 {
		s.mu.Unlock()
		return domain.Transcript{}, domain.ErrNotFound
	}
	if provider != "" && provider != v.input.Provider {
		s.mu.Unlock()
		return domain.Transcript{}, domain.ErrProviderChanged
	}
	if v.claimed {
		s.mu.Unlock()
		return s.replaySessionTranscription(ctx, user, v.input.Provider, requestID, id, audio)
	}
	v.claimed = true
	if v.timer != nil {
		v.timer.Stop()
	}
	s.mu.Unlock()
	defer func() { v.cancel(); v.release(); s.mu.Lock(); delete(s.sessions, id); s.mu.Unlock() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(v.ctx, cancel)
	defer stop()
	return s.transcribe(ctx, user, v.input.Provider, requestID, audio, v, id)
}

func (s *Service) replaySessionTranscription(ctx context.Context, user uint, provider, requestID, id string, audio domain.Audio) (domain.Transcript, error) {
	if requestID == "" || len(requestID) > 128 || id == "" || s.store == nil {
		return domain.Transcript{}, domain.ErrNotFound
	}
	record, err := s.store.Get(ctx, digest("asr", userKey(user), provider, requestID))
	if err != nil {
		return domain.Transcript{}, err
	}
	hash := sha256.Sum256(audio.Bytes)
	if record.UserID != user || record.OperationID != id {
		return domain.Transcript{}, domain.ErrNotFound
	}
	if record.Fingerprint != hex.EncodeToString(hash[:]) {
		return domain.Transcript{}, domain.ErrConflict
	}
	if record.Status != "complete" {
		return domain.Transcript{}, domain.ErrUncertain
	}
	return domain.Transcript{SessionID: id, Text: record.Text, DurationMS: record.DurationMS, Sequence: 1, Final: true}, nil
}
