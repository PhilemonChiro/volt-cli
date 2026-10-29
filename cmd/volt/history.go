package main

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/PhilemonChiro/volt-cli/internal/history"
)

// runHistory: volt history record|log|show|blame|restore|undo <project> …
// Every subcommand prints JSON (the IDE and `volt mcp` drive it).
func runHistory(args []string) int {
	if len(args) < 2 {
		stderr.Println("Usage: volt history record|log|show|blame|restore|undo <project> [--author a] [--message m] [--file f] [--hash h] [--limit n]")
		return 2
	}
	sub, dir := args[0], args[1]
	fl, _ := newFlags("history "+sub, "<project>")
	author := fl.String("author", "", "who made the change")
	message := fl.String("message", "", "what changed")
	file := fl.String("file", "", "a file in the project")
	hash := fl.String("hash", "", "a recorded change")
	limit := fl.Int("limit", 50, "most entries to list")
	coalesce := fl.Bool("coalesce", false, "record: fold into the last change if the same author saved it moments ago")
	if _, ok := parse(fl, args[2:]); !ok {
		return 2
	}
	out, err := historyCmd(sub, dir, *author, *message, *file, *hash, *limit, *coalesce)
	enc := json.NewEncoder(os.Stdout)
	if err != nil {
		enc.Encode(map[string]string{"error": err.Error()})
		return 1
	}
	enc.Encode(out)
	return 0
}

func historyCmd(sub, dir, author, message, file, hash string, limit int, coalesce bool) (any, error) {
	h, err := history.Open(dir)
	if err != nil {
		return nil, err
	}
	need := func(v, name string) error {
		if v == "" {
			return errors.New("--" + name + " is required")
		}
		return nil
	}
	switch sub {
	case "record":
		c, err := h.Record(author, message, coalesce)
		return map[string]any{"hash": c, "recorded": c != ""}, err
	case "log":
		return h.Log(file, limit)
	case "show":
		if err := need(hash, "hash"); err != nil {
			return nil, err
		}
		return h.Show(hash)
	case "blame":
		if err := need(file, "file"); err != nil {
			return nil, err
		}
		return h.Blame(file)
	case "restore":
		if err := need(hash, "hash"); err != nil {
			return nil, err
		}
		c, err := h.Restore(hash, file, author)
		return map[string]any{"hash": c}, err
	case "undo":
		if err := need(hash, "hash"); err != nil {
			return nil, err
		}
		c, err := h.Undo(hash, author)
		return map[string]any{"hash": c}, err
	}
	return nil, errors.New("unknown subcommand " + sub)
}
