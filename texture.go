package main

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"

	"github.com/go-gl/gl/v4.1-core/gl"
)

func LoadTexture(name, file string) error {
	imgFile, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("texture %q not found on disk: %v", file, err)
	}
	img, _, err := image.Decode(imgFile)
	if err != nil {
		return err
	}

	rgba := image.NewRGBA(img.Bounds())
	if rgba.Stride != rgba.Rect.Size().X*4 {
		return fmt.Errorf("unsupported stride")
	}
	draw.Draw(rgba, rgba.Bounds(), img, image.Point{0, 0}, draw.Src)

	var texture uint32
	gl.GenTextures(1, &texture)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, texture)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.REPEAT)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.REPEAT)
	gl.TexImage2D(
		gl.TEXTURE_2D,
		0,
		gl.RGBA,
		int32(rgba.Rect.Size().X),
		int32(rgba.Rect.Size().Y),
		0,
		gl.RGBA,
		gl.UNSIGNED_BYTE,
		gl.Ptr(rgba.Pix))

	Textures[name] = texture
	return nil
}

// ensureTextureLoaded uploads the named texture to the GPU if it isn't
// already resident. Level loading (loadRawChunk) walks every referenced
// texture and loads it up front, but editor operations that build Wall/
// Floor/Ceiling/Primitive structs directly (e.g. createChunkAtCamera,
// createSectorFromPolygon) bypass that pipeline. Without this, drawing with
// an unloaded name binds texture 0 (gl.BindTexture(TEXTURE_2D, 0)), which
// samples as black — the geometry renders, but pure black, regardless of
// how much light is in the scene.
func ensureTextureLoaded(name string) error {
	if name == "" {
		return nil
	}
	if _, ok := Textures[name]; ok {
		return nil
	}
	path, ok := textureNames[name]
	if !ok {
		return fmt.Errorf("unknown texture %q — ensure a matching .png exists in textures/", name)
	}
	return LoadTexture(name, filepath.Join(executablePath, path))
}
