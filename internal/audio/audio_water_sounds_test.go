package audio

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestWaterSoundsAreSeamlessAndShipped checks every generated water clip:
// in range, loops meeting themselves with no click, and the shipped file
// under resources/sounds the same as the generator makes today. With
// FOUNDRY_WRITE_WATER_SOUNDS=1 it writes the files instead (the file
// comment of audio_water_sounds.go).
func TestWaterSoundsAreSeamlessAndShipped(t *testing.T) {
	write := os.Getenv("FOUNDRY_WRITE_WATER_SOUNDS") == "1"
	dir := filepath.Join("..", "..", "resources", "sounds")
	for _, name := range WaterSoundNames {
		s, ok := GenerateWaterSound(name)
		if !ok || len(s.Samples) == 0 {
			t.Fatalf("%s: not generated", name)
		}
		peak, sum := 0.0, 0.0
		for _, x := range s.Samples {
			peak = math.Max(peak, math.Abs(float64(x)))
			sum += float64(x) * float64(x)
		}
		rms := math.Sqrt(sum / float64(len(s.Samples)))
		if peak > 0.9 || rms < 0.02 {
			t.Errorf("%s: peak %.2f rms %.3f; want audible and under 0.9", name, peak, rms)
		}
		if s.Loop {
			// The jump from the last sample to the first is no bigger than
			// the clip's ordinary sample-to-sample steps.
			maxStep := 0.0
			for i := 1; i < len(s.Samples); i++ {
				maxStep = math.Max(maxStep, math.Abs(float64(s.Samples[i]-s.Samples[i-1])))
			}
			seam := math.Abs(float64(s.Samples[0] - s.Samples[len(s.Samples)-1]))
			if seam > maxStep {
				t.Errorf("%s: seam step %.3f beyond the largest step inside %.3f", name, seam, maxStep)
			}
		}
		want := EncodeWAV16(s.Samples, s.Rate)
		path := filepath.Join(dir, name+".wav")
		if write {
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: not shipped (%v); regenerate with FOUNDRY_WRITE_WATER_SOUNDS=1", name, err)
			continue
		}
		// Within a couple of LSBs: the maths library may round a last bit
		// differently on another platform.
		if len(got) != len(want) || !bytes.Equal(got[:44], want[:44]) {
			t.Errorf("%s: the shipped file differs from the generator; regenerate with FOUNDRY_WRITE_WATER_SOUNDS=1", name)
			continue
		}
		for i := 44; i+1 < len(got); i += 2 {
			a := int(int16(uint16(got[i]) | uint16(got[i+1])<<8))
			b := int(int16(uint16(want[i]) | uint16(want[i+1])<<8))
			if a-b > 2 || b-a > 2 {
				t.Errorf("%s: the shipped file differs from the generator at byte %d; regenerate with FOUNDRY_WRITE_WATER_SOUNDS=1", name, i)
				break
			}
		}
	}
}
