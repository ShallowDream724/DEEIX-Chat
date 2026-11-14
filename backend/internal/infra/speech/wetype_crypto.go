package speech

import (
	"bytes"
	"crypto/aes"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	"math/big"
	"strings"
)

func aesEncrypt(key, input []byte) ([]byte, error) {
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	pad := aes.BlockSize - len(input)%aes.BlockSize
	p := append(append([]byte{}, input...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	for n := 0; n < len(p); n += aes.BlockSize {
		block.Encrypt(p[n:n+aes.BlockSize], p[n:n+aes.BlockSize])
	}
	return p, nil
}
func aesDecrypt(key, input []byte) ([]byte, error) {
	if len(input) == 0 || len(input)%aes.BlockSize != 0 {
		return nil, domain.ErrInvalid
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	p := append([]byte(nil), input...)
	for n := 0; n < len(p); n += aes.BlockSize {
		block.Decrypt(p[n:n+aes.BlockSize], p[n:n+aes.BlockSize])
	}
	pad := int(p[len(p)-1])
	if pad < 1 || pad > 16 || pad > len(p) || !bytes.Equal(p[len(p)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
		return nil, domain.ErrInvalid
	}
	return p[:len(p)-pad], nil
}
func curve128() *elliptic.CurveParams {
	num := func(s string) *big.Int { n, _ := new(big.Int).SetString(s, 16); return n }
	return &elliptic.CurveParams{Name: "secp128r1", BitSize: 128, P: num("FFFFFFFDFFFFFFFFFFFFFFFFFFFFFFFF"), N: num("FFFFFFFE0000000075A30D1B9038A115"), B: num("E87579C11079F43DD824993C2CEE5ED3"), Gx: num("161FF7528B899B2D0C28607CA52C5B86"), Gy: num("CF5AC8395BAFEB13C02DA292DDED7A83")}
}
func weTypeKeypair() ([]byte, string, error) {
	curve := curve128()
	priv, x, y, e := elliptic.GenerateKey(curve, rand.Reader)
	if e != nil {
		return nil, "", e
	}
	return priv, strings.ToUpper(hex.EncodeToString(elliptic.Marshal(curve, x, y))), nil
}
func weTypeShared(private []byte, public string) ([]byte, error) {
	b, e := hex.DecodeString(public)
	if e != nil {
		return nil, domain.ErrInvalid
	}
	curve := curve128()
	x, y := elliptic.Unmarshal(curve, b)
	if x == nil {
		return nil, domain.ErrInvalid
	}
	sx, _ := curve.ScalarMult(x, y, private)
	if sx == nil {
		return nil, domain.ErrInvalid
	}
	return []byte(strings.ToUpper(hex.EncodeToString(sx.FillBytes(make([]byte, 16))))), nil
}

// Snappy literal encoding is sufficient upstream; decoding supports all copy
// tags and validates the declared bound before allocating any output memory.
func snappyEncode(b []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(len(b)))
	for len(b) > 0 {
		n := len(b)
		if n > 60 {
			n = 60
		}
		out = append(out, byte((n-1)<<2))
		out = append(out, b[:n]...)
		b = b[n:]
	}
	return out
}
func snappyDecode(b []byte) ([]byte, error) {
	size, n := binary.Uvarint(b)
	if n <= 0 || size > 4<<20 {
		return nil, domain.ErrInvalid
	}
	b = b[n:]
	out := make([]byte, 0, int(size))
	for len(b) > 0 {
		tag := b[0]
		b = b[1:]
		length := 0
		offset := 0
		if tag&3 == 0 {
			length = int(tag >> 2)
			if length >= 60 {
				width := length - 59
				if len(b) < width {
					return nil, domain.ErrInvalid
				}
				length = 0
				for i := 0; i < width; i++ {
					length |= int(b[i]) << uint(8*i)
				}
				b = b[width:]
			}
			length++
			if length > len(b) || len(out)+length > int(size) {
				return nil, domain.ErrInvalid
			}
			out = append(out, b[:length]...)
			b = b[length:]
			continue
		}
		switch tag & 3 {
		case 1:
			if len(b) < 1 {
				return nil, domain.ErrInvalid
			}
			length = int((tag>>2)&7) + 4
			offset = int(tag>>5)<<8 | int(b[0])
			b = b[1:]
		case 2:
			if len(b) < 2 {
				return nil, domain.ErrInvalid
			}
			length = int(tag>>2) + 1
			offset = int(binary.LittleEndian.Uint16(b))
			b = b[2:]
		case 3:
			if len(b) < 4 {
				return nil, domain.ErrInvalid
			}
			length = int(tag>>2) + 1
			offset = int(binary.LittleEndian.Uint32(b))
			b = b[4:]
		}
		if offset <= 0 || offset > len(out) || len(out)+length > int(size) {
			return nil, domain.ErrInvalid
		}
		for i := 0; i < length; i++ {
			out = append(out, out[len(out)-offset])
		}
	}
	if len(out) != int(size) {
		return nil, domain.ErrInvalid
	}
	return out, nil
}
