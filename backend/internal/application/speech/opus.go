package speech

import domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"

// RFC 6716 section 3: reject packets whose encoded sample duration differs from
// the negotiated 20ms. Counting browser messages alone would let long Opus
// packets bypass recording and billing limits.
func validOpus20ms(packet []byte) bool {
	if len(packet) == 0 || len(packet) > 1275 || packet[0]&4 != 0 {
		return false
	}
	samples, err := domain.OpusSamples48k(packet)
	return err == nil && samples == 960
}
