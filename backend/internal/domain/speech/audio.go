package speech

// OpusSamples48k reads the TOC-defined decoded packet duration (RFC 6716).
// The result always uses Opus's 48kHz timeline, independent of capture rate.
func OpusSamples48k(packet []byte) (int, error) {
	if len(packet) == 0 || len(packet) > 64<<10 {
		return 0, ErrInvalid
	}
	config := packet[0] >> 3
	var samples int
	switch {
	case config >= 16:
		samples = 120 << uint(config&3)
	case config >= 12:
		samples = 480 << uint(config&1)
	default:
		samples = []int{480, 960, 1920, 2880}[config&3]
	}
	frames := 1
	switch packet[0] & 3 {
	case 1, 2:
		frames = 2
	case 3:
		if len(packet) < 2 {
			return 0, ErrInvalid
		}
		frames = int(packet[1] & 63)
	}
	if frames == 0 || samples*frames > 5760 {
		return 0, ErrInvalid
	}
	return samples * frames, nil
}
