package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"github.com/lizhaojie/tvbot/internal/config"
	"github.com/lizhaojie/tvbot/internal/store"
)

// runSeedUser is the "seed-user" sub-command. It reads a username and password
// from stdin (password hidden via terminal raw mode), bcrypt-hashes the password,
// and inserts the user into the users table. Safe to call multiple times for the
// same username — it will error on duplicate (UNIQUE constraint).
func runSeedUser(cfg *config.Config) {
	ctx := context.Background()

	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "seed-user: connect db: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Username: ")
	username, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "seed-user: read username: %v\n", err)
		os.Exit(1)
	}
	username = strings.TrimSpace(username)
	if username == "" {
		fmt.Fprintln(os.Stderr, "seed-user: username cannot be empty")
		os.Exit(1)
	}

	fmt.Print("Password: ")
	rawPass, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println() // newline after hidden input
	if err != nil {
		fmt.Fprintf(os.Stderr, "seed-user: read password: %v\n", err)
		os.Exit(1)
	}
	if len(rawPass) == 0 {
		fmt.Fprintln(os.Stderr, "seed-user: password cannot be empty")
		os.Exit(1)
	}

	hash, err := bcrypt.GenerateFromPassword(rawPass, bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "seed-user: bcrypt: %v\n", err)
		os.Exit(1)
	}

	userRepo := store.NewUserRepo(pool)
	if err := userRepo.Create(ctx, pool, username, string(hash)); err != nil {
		fmt.Fprintf(os.Stderr, "seed-user: insert user: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("seed-user: created user %q\n", username)
}
