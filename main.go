// Command blog serves a small Markdown blog with an RSS feed and a
// password-protected admin panel. Posts and settings live in one JSON file
// under DATA_DIR; uploaded images, resized on upload, sit in DATA_DIR/images.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "hashpw":
			runHashpw()
			return
		case "import":
			if err := runImport(os.Args[2:]); err != nil {
				log.Fatal(err)
			}
			return
		case "rename-images":
			if err := runRenameImages(os.Args[2:]); err != nil {
				log.Fatal(err)
			}
			return
		case "merge":
			if err := runMerge(os.Args[2:]); err != nil {
				log.Fatal(err)
			}
			return
		}
	}

	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	app, err := newApp(cfg)
	if err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           app.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// Uploads of large photos over a slow uplink.
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go app.runSender(ctx)
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()

	log.Printf("blog %s listening on %s, data in %s", version, cfg.Listen, cfg.DataDir)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// runHashpw reads a password from stdin and prints the ADMIN_PASSWORD_HASH line.
// On a terminal the input is hidden with stty (busybox has it in the image).
func runHashpw() {
	fmt.Fprint(os.Stderr, "Admin password: ")
	hidden := stty("-echo") == nil
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if hidden {
		stty("echo")
		fmt.Fprintln(os.Stderr)
	}
	if err != nil && line == "" {
		fmt.Fprintln(os.Stderr, "no password read")
		os.Exit(1)
	}
	pw := strings.TrimRight(line, "\r\n")
	if len(pw) < 8 {
		fmt.Fprintln(os.Stderr, "password must be at least 8 characters")
		os.Exit(1)
	}
	h, err := hashPassword(pw)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("ADMIN_PASSWORD_HASH=" + h)
}

// stty runs stty on the controlling terminal; it fails when stdin is not one.
func stty(arg string) error {
	cmd := exec.Command("stty", arg)
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
