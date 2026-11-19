package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Zubrium/cdn/main/internal/buildprofile"
)

func main() {
	profilePath := flag.String("profile", "", "build profile YAML")
	outputPath := flag.String("out", "", "generated Go output")
	packageName := flag.String("package", "main", "generated Go package name")
	buildTag := flag.String("tag", "", "optional Go build-tag expression")
	flag.Parse()
	if flag.NArg() != 0 || *profilePath == "" || *outputPath == "" {
		fmt.Fprintln(os.Stderr, "usage: profilegen -profile <yaml> -out <go-file> [-package main]")
		os.Exit(2)
	}
	if err := generate(*profilePath, *outputPath, *packageName, *buildTag); err != nil {
		fmt.Fprintln(os.Stderr, "profile generation failed:", err)
		os.Exit(1)
	}
}

func generate(profilePath, outputPath, packageName, buildTag string) error {
	input, err := os.Open(profilePath)
	if err != nil {
		return errors.New("cannot open build profile")
	}
	defer input.Close()
	plan, err := buildprofile.ParseBuildProfile(input)
	if err != nil {
		return err
	}
	source, err := buildprofile.GenerateBuildProfileGoWithTag(plan, packageName, buildTag)
	if err != nil {
		return err
	}
	return writeFileAtomic(outputPath, source)
}

func writeFileAtomic(path string, data []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".zubrium-profile-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := io.Copy(temporary, bytes.NewReader(data)); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err == nil {
		err = dir.Sync()
		_ = dir.Close()
	}
	return err
}
