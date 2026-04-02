package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// LoadOBJ parses a Wavefront OBJ file and returns a flat vertex buffer [X,Y,Z,U,V,...].
// Handles v, vt, f directives; triangulates quads via fan; skips unknown lines.
// No element arrays — flat list matches the existing Geometry design.
func LoadOBJ(path string) ([]float32, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var positions [][3]float32
	var texCoords [][2]float32
	var result []float32

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		switch parts[0] {
		case "v":
			if len(parts) < 4 {
				continue
			}
			x, err := strconv.ParseFloat(parts[1], 32)
			if err != nil {
				return nil, fmt.Errorf("OBJ: invalid vertex X %q in %s: %w", parts[1], path, err)
			}
			y, err := strconv.ParseFloat(parts[2], 32)
			if err != nil {
				return nil, fmt.Errorf("OBJ: invalid vertex Y %q in %s: %w", parts[2], path, err)
			}
			z, err := strconv.ParseFloat(parts[3], 32)
			if err != nil {
				return nil, fmt.Errorf("OBJ: invalid vertex Z %q in %s: %w", parts[3], path, err)
			}
			positions = append(positions, [3]float32{float32(x), float32(y), float32(z)})

		case "vt":
			if len(parts) < 3 {
				continue
			}
			u, err := strconv.ParseFloat(parts[1], 32)
			if err != nil {
				return nil, fmt.Errorf("OBJ: invalid texcoord U %q in %s: %w", parts[1], path, err)
			}
			v, err := strconv.ParseFloat(parts[2], 32)
			if err != nil {
				return nil, fmt.Errorf("OBJ: invalid texcoord V %q in %s: %w", parts[2], path, err)
			}
			texCoords = append(texCoords, [2]float32{float32(u), float32(v)})

		case "f":
			if len(parts) < 4 {
				continue
			}
			type faceVert struct{ vi, ti int }
			fverts := make([]faceVert, 0, len(parts)-1)
			for _, token := range parts[1:] {
				toks := strings.Split(token, "/")
				vi, err := strconv.Atoi(toks[0])
				if err != nil {
					return nil, fmt.Errorf("OBJ: invalid face vertex index %q in %s: %w", toks[0], path, err)
				}
				ti := 0
				if len(toks) >= 2 && toks[1] != "" {
					var terr error
					ti, terr = strconv.Atoi(toks[1])
					if terr != nil {
						return nil, fmt.Errorf("OBJ: invalid face texcoord index %q in %s: %w", toks[1], path, terr)
					}
				}
				fverts = append(fverts, faceVert{vi, ti})
			}
			// Triangle fan from vertex 0
			for i := 1; i < len(fverts)-1; i++ {
				for _, fv := range []faceVert{fverts[0], fverts[i], fverts[i+1]} {
					vi := fv.vi - 1
					if vi < 0 || vi >= len(positions) {
						return nil, fmt.Errorf("OBJ: invalid vertex index %d in %s", fv.vi, path)
					}
					p := positions[vi]
					result = append(result, p[0], p[1], p[2])
					ti := fv.ti - 1
					if ti >= 0 && ti < len(texCoords) {
						tc := texCoords[ti]
						result = append(result, tc[0], tc[1])
					} else {
						result = append(result, 0, 0)
					}
				}
			}
		}
	}
	return result, scanner.Err()
}
