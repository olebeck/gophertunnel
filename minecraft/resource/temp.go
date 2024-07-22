package resource

import (
	"archive/zip"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"strings"
)

type countWriter struct {
	count int
}

func (cw *countWriter) Write(p []byte) (n int, err error) {
	cw.count += len(p)
	return len(p), nil
}

// createTempArchive creates a zip archive from the files in the path passed and writes it to a temporary
// file, which is returned when successful.
func createTempArchive(filesystem fs.FS) (*os.File, int, [32]byte, error) {
	temp, err := createTempFile()
	if err != nil {
		return nil, 0, [32]byte{}, err
	}
	h := sha256.New()
	cw := &countWriter{}
	writer := zip.NewWriter(io.MultiWriter(temp, h, cw))
	if err := fs.WalkDir(filesystem, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Make sure to replace backslashes with forward slashes as Go zip only allows that.
		path = strings.Replace(path, `\`, "/", -1)
		// Always ignore '.' as it is not a real file/folder.
		if path == "." {
			return nil
		}
		if d.IsDir() {
			// This is a directory: Go zip requires you add forward slashes at the end to create directories.
			_, _ = writer.Create(path + "/")
			return nil
		}
		f, err := writer.Create(path)
		if err != nil {
			return fmt.Errorf("create new zip file: %w", err)
		}
		file, err := filesystem.Open(path)
		if err != nil {
			return fmt.Errorf("open resource pack file %v: %w", path, err)
		}
		// Write the original content into the 'zip file' so that we write compressed data to the file.
		if _, err := io.Copy(f, file); err != nil {
			return fmt.Errorf("write file data to zip: %w", err)
		}
		_ = file.Close()
		return nil
	}); err != nil {
		return nil, 0, [32]byte{}, fmt.Errorf("build zip archive: %w", err)
	}
	_ = writer.Close()
	return temp, cw.count, [32]byte(h.Sum(nil)), nil
}

// createTempFile attempts to create a temporary file and returns it.
func createTempFile() (*os.File, error) {
	// We've got a directory which we need to load. Provided we need to send compressed zip data to the
	// client, we compile it to a zip archive in a temporary file.

	// Note that we explicitly do not handle the error here. If the user config
	// dir cannot be found, 'dir' will be an empty string. os.CreateTemp will
	// then use the default temporary file directory, which might succeed in
	// this case.
	dir, _ := os.UserConfigDir()
	_ = os.MkdirAll(dir, os.ModePerm)

	temp, err := createTemp(fmt.Sprintf("temp_resource_pack-%d.mcpack", rand.Int63()))
	if err != nil {
		return nil, fmt.Errorf("create temp resource pack file: %w", err)
	}
	err = os.Remove(temp.Name())
	if err != nil {
		return nil, fmt.Errorf("remove temp resource pack file: %w", err)
	}
	return temp, nil
}
