package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// outputDirPerm is the permission mode used when creating the output
	// directory for --output.
	outputDirPerm = 0o750
	// outputFilePerm is the mode a new output file is created with before the
	// umask applies, as os.Create does it.
	outputFilePerm fs.FileMode = 0o666
)

// errOutputIsInput is returned when the output is one of the bundle's inputs.
var errOutputIsInput = errors.New("refusing to overwrite an input")

// putOutput delivers the finished bundle: to stdout for "-", otherwise to the
// file at path, which must not be one of inputs. It is called only once the
// build has succeeded, so a failed build prints nothing and changes nothing on
// disk: not an input, not a previous bundle, not a directory.
func putOutput(path string, data []byte, inputs []string) error {
	if path == "-" {
		_, err := os.Stdout.Write(data)
		if err != nil {
			return fmt.Errorf("write output: %w", err)
		}

		return nil
	}

	err := checkNotInput(path, inputs)
	if err != nil {
		return err
	}

	return writeOutput(path, data)
}

// checkNotInput returns an error when path names an existing regular file
// that is one of inputs, so writing the bundle there would destroy a file the
// bundle is built from. Files are compared by identity (os.SameFile), which
// also catches a differently spelled path, a symlink or a hard link to an
// input. An input that does not exist, such as a config file that was never
// there, cannot be overwritten and is skipped.
func checkNotInput(path string, inputs []string) error {
	output, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("check output: %w", err)
	}

	if !output.Mode().IsRegular() {
		// Writing to a device such as /dev/null, or to a FIFO, destroys
		// nothing, even when the same device was read as an input.
		return nil
	}

	for _, input := range inputs {
		info, statErr := os.Stat(input)
		if errors.Is(statErr, fs.ErrNotExist) {
			continue
		}

		if statErr != nil {
			return fmt.Errorf("check output: %w", statErr)
		}

		if os.SameFile(output, info) {
			return fmt.Errorf("%w: output %s is the same file as input %s", errOutputIsInput, path, input)
		}
	}

	return nil
}

// writeOutput writes the finished bundle to path, creating missing parent
// directories. The file is written in place, as os.Create did: a symlink is
// followed and its target rewritten (a dangling link's target is created), a
// device such as /dev/null is written to, and an existing file keeps its
// permission bits.
func writeOutput(path string, data []byte) error {
	err := os.MkdirAll(filepath.Dir(path), outputDirPerm)
	if err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	err = os.WriteFile(path, data, outputFilePerm)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}
