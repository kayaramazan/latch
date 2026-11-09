package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/kayaramazan/latch/latchd/internal/api"
	"github.com/kayaramazan/latch/latchd/internal/config"
	"github.com/kayaramazan/latch/latchd/internal/store"
	"github.com/kayaramazan/latch/proto/model"
)

// Auth dispatches `latchd auth ...`.
func Auth(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: latchd auth <set-password|disable> ...")
	}
	switch args[0] {
	case "set-password":
		return authSetPassword(args[1:])
	case "disable":
		return authDisable(args[1:])
	default:
		return fmt.Errorf("auth: unknown subcommand %q", args[0])
	}
}

func authSetPassword(args []string) error {
	fs := flag.NewFlagSet("auth set-password", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "data directory")
	pw := fs.String("password", "", "password (omit to read from the terminal)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	password := *pw
	if password == "" {
		p, err := readPassword()
		if err != nil {
			return err
		}
		password = p
	}
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	hash, err := api.HashPassword(password)
	if err != nil {
		return err
	}
	return withStore(*dataDir, func(st *store.Store) error {
		_, err := st.Update(context.Background(), "cli", "set management password", func(snap *model.Snapshot) error {
			snap.Settings.Auth = model.AuthSettings{Mode: model.AuthPassword, PasswordHash: hash}
			return nil
		})
		if err != nil {
			return err
		}
		fmt.Println("Management password set. Authentication is now required.")
		fmt.Println("Restart latchd for a non-loopback listener to take effect.")
		return nil
	})
}

func authDisable(args []string) error {
	fs := flag.NewFlagSet("auth disable", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withStore(*dataDir, func(st *store.Store) error {
		_, err := st.Update(context.Background(), "cli", "disable auth", func(snap *model.Snapshot) error {
			snap.Settings.Auth = model.AuthSettings{Mode: model.AuthNone}
			return nil
		})
		if err != nil {
			return err
		}
		fmt.Println("Authentication disabled (loopback only).")
		return nil
	})
}

// withStore opens the latchd store at dataDir and runs fn.
func withStore(dataDir string, fn func(*store.Store) error) error {
	paths, err := config.Resolve(dataDir)
	if err != nil {
		return err
	}
	st, err := store.Open(paths.DBPath, store.Options{})
	if err != nil {
		return err
	}
	defer st.Close()
	return fn(st)
}

func readPassword() (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Print("New management password: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		return string(b), err
	}
	// Non-interactive: read one line.
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line), err
}
