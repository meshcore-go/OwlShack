package web

import (
	"embed"
	"io/fs"
)

//go:embed frontend/dist/*
var assets embed.FS

func Assets() fs.FS {
	sub, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		panic(err)
	}
	return sub
}
