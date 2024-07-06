package resource

import (
	"archive/zip"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
)

// createTempArchive creates a zip archive from the files in the path passed and writes it to a temporary
// file, which is returned when successful.
func createTempArchive(path string) (*os.File, error) {
	temp, err := createTempFile()
	if err != nil {
		return nil, err
	}
	writer := zip.NewWriter(temp)
	if err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(path, filePath)
		if err != nil {
			return fmt.Errorf("find relative path: %w", err)
		}
		// Make sure to replace backslashes with forward slashes as Go zip only allows that.
		relPath = strings.Replace(relPath, `\`, "/", -1)
		// Always ignore '.' as it is not a real file/folder.
		if relPath == "." {
			return nil
		}
		s, err := os.Stat(filePath)
		if err != nil {
			return fmt.Errorf("read stat of file path %v: %w", filePath, err)
		}
		if s.IsDir() {
			// This is a directory: Go zip requires you add forward slashes at the end to create directories.
			_, _ = writer.Create(relPath + "/")
			return nil
		}
		f, err := writer.Create(relPath)
		if err != nil {
			return fmt.Errorf("create new zip file: %w", err)
		}
		file, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("open resource pack file %v: %w", filePath, err)
		}
		data, _ := io.ReadAll(file)
		// Write the original content into the 'zip file' so that we write compressed data to the file.
		if _, err := f.Write(data); err != nil {
			return fmt.Errorf("write file data to zip: %w", err)
		}
		_ = file.Close()
		return nil
	}); err != nil {
		return nil, fmt.Errorf("build zip archive: %w", err)
	}
	_ = writer.Close()
	return temp, nil
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
	return temp, nil
}
