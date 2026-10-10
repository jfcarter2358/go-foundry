package tracker

// wav.go — writing PCM. Reading goes through internal/audio's decoders.

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// WriteWAV writes interleaved float samples as a RIFF WAV: 16- or 24-bit PCM,
// or 32-bit float. Values are clipped to [-1, 1].
func WriteWAV(w io.Writer, data []float32, channels, rate, bits int) error {
	if channels < 1 || rate < 1 {
		return fmt.Errorf("wav: %d channels at %d Hz", channels, rate)
	}
	format := uint16(1)
	switch bits {
	case 16, 24:
	case 32:
		format = 3
	default:
		return fmt.Errorf("wav: unsupported bit depth %d", bits)
	}
	bps := bits / 8
	dataLen := len(data) * bps
	bw := bufio.NewWriter(w)
	le := binary.LittleEndian
	hdr := make([]byte, 44)
	copy(hdr[0:], "RIFF")
	le.PutUint32(hdr[4:], uint32(36+dataLen))
	copy(hdr[8:], "WAVEfmt ")
	le.PutUint32(hdr[16:], 16)
	le.PutUint16(hdr[20:], format)
	le.PutUint16(hdr[22:], uint16(channels))
	le.PutUint32(hdr[24:], uint32(rate))
	le.PutUint32(hdr[28:], uint32(rate*channels*bps))
	le.PutUint16(hdr[32:], uint16(channels*bps))
	le.PutUint16(hdr[34:], uint16(bits))
	copy(hdr[36:], "data")
	le.PutUint32(hdr[40:], uint32(dataLen))
	if _, err := bw.Write(hdr); err != nil {
		return err
	}
	var buf [4]byte
	for _, v := range data {
		if v != v { // NaN
			v = 0
		}
		v = clamp32(v, -1, 1)
		switch bits {
		case 16:
			le.PutUint16(buf[:], uint16(int16(math.Round(float64(v)*32767))))
		case 24:
			x := int32(math.Round(float64(v) * 8388607))
			buf[0], buf[1], buf[2] = byte(x), byte(x>>8), byte(x>>16)
		case 32:
			le.PutUint32(buf[:], math.Float32bits(v))
		}
		if _, err := bw.Write(buf[:bps]); err != nil {
			return err
		}
	}
	return bw.Flush()
}
