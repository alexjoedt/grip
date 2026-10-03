package main

import (
	"context"
	"os"
	"testing"
)

func TestReadOnlyCommandsCreateNoFiles(t *testing.T) {
	for _, args := range [][]string{{"grip", "version"}, {"grip", "--help"}, {"grip", "--version"}} {
		t.Run(args[1], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)

			if err := newApp().Run(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(home)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("%v created %d entries in HOME, want 0", args, len(entries))
			}
		})
	}
}

func TestListOnEmptyHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := newApp().Run(context.Background(), []string{"grip", "ls"}); err != nil {
		t.Fatal(err)
	}
}
