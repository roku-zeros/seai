// seaid is the sole runtime database writer for a project.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"seai.local/seai/internal/application"
	"seai.local/seai/internal/indexer"
	"seai.local/seai/internal/storage"
	"seai.local/seai/internal/transports"
	"seai.local/seai/internal/workspace"
	"syscall"
	"time"
)

func main() {
	if e := run(); e != nil {
		log.Print(e)
		os.Exit(4)
	}
}
func run() error {
	repo := flag.String("repo", "", "Git repository root")
	data := flag.String("data", "", "private runtime directory outside repository")
	project := flag.String("project", "", "explicit project identity")
	flag.Parse()
	if *repo == "" || *data == "" || *project == "" {
		return fmt.Errorf("--repo, --data and --project are required")
	}
	root, d, e := application.ValidateLocations(*repo, *data)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(d, 0700); e != nil {
		return e
	}
	info, e := os.Stat(d)
	if e != nil {
		return e
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("runtime directory must be owned by current user and mode 0700")
	}
	lock, e := os.OpenFile(filepath.Join(d, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return fmt.Errorf("DAEMON_ALREADY_RUNNING: %w", e)
	}
	store, e := storage.Open(d)
	if e != nil {
		return e
	}
	defer store.DB.Close()
	if e = store.Register(*project, root); e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Join(d, "indexes"), 0700); e != nil {
		return e
	}
	build, e := indexer.BuildProfile(context.Background())
	if e != nil {
		return e
	}
	s := &application.Service{Store: store, Data: d, Workspace: workspace.Service{Root: root, Project: *project, Objects: storage.CAS{Root: d}, Build: build}}
	sock := filepath.Join(d, "seaid.sock")
	if len(sock) > 100 {
		return fmt.Errorf("socket path too long; choose a shorter --data directory")
	}
	if e = os.Remove(sock); e != nil && !os.IsNotExist(e) {
		return e
	}
	// Umask protects the socket from the moment of creation, before chmod.
	old := syscall.Umask(0077)
	listener, e := net.Listen("unix", sock)
	syscall.Umask(old)
	if e != nil {
		return e
	}
	defer os.Remove(sock)
	if e = os.Chmod(sock, 0600); e != nil {
		listener.Close()
		return e
	}
	server := &http.Server{Handler: transports.Handler(s), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 30 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("seaid 0.1.0-dev project=%s socket=%s", *project, sock)
	e = server.Serve(listener)
	s.Wait()
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
