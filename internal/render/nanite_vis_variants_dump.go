package render

// nanite_vis_variants_dump.go — FOUNDRY_DUMP_MAT_PROGRAMS=<dir> writes each
// material program's source and the driver's program binary there, for
// reading what a variant costs (NVIDIA's binary carries its assembly, whose
// trailer counts the instructions and registers; docs/adventure/PHASE_F.md).
// A measuring tool only: nothing reads the files back.

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/go-gl/gl/v4.3-core/gl"
)

var dumpMatPrograms = os.Getenv("FOUNDRY_DUMP_MAT_PROGRAMS")

// naniteMatProfileKeys times each material variant's bins as its own
// profile section (FOUNDRY_NANITE_MAT_PROFILE=1), a measuring tool too.
var naniteMatProfileKeys = os.Getenv("FOUNDRY_NANITE_MAT_PROFILE") == "1"

// dumpMaterialProgram writes prog (variant key k) and its source to the dump
// directory, when one is set.
func dumpMaterialProgram(k uint64, prog uint32, src string) {
	if dumpMatPrograms == "" || prog == 0 {
		return
	}
	if err := os.MkdirAll(dumpMatPrograms, 0o755); err != nil {
		log.Printf("dump: %v", err)
		return
	}
	base := filepath.Join(dumpMatPrograms, fmt.Sprintf("mat_%012x", k))
	_ = os.WriteFile(base+".glsl", []byte(src), 0o644)
	var n int32
	gl.GetProgramiv(prog, gl.PROGRAM_BINARY_LENGTH, &n)
	if n <= 0 {
		return
	}
	buf := make([]byte, n)
	var format uint32
	var got int32
	gl.GetProgramBinary(prog, n, &got, &format, gl.Ptr(buf))
	_ = os.WriteFile(base+".bin", buf[:got], 0o644)
}
