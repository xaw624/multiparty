// Comando multiparty: servidor web de la playlist compartida.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"

	"github.com/xaw624/multiparty/internal/cifrado"
	"github.com/xaw624/multiparty/internal/config"
	"github.com/xaw624/multiparty/internal/db"
	"github.com/xaw624/multiparty/internal/resolver"
	"github.com/xaw624/multiparty/internal/spotify"
	"github.com/xaw624/multiparty/internal/tareas"
	"github.com/xaw624/multiparty/internal/web"
	"github.com/xaw624/multiparty/internal/youtube"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fallo fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := db.Open(cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer database.Close()

	if err := db.Migrate(ctx, database); err != nil {
		return err
	}

	if cfg.SecretoGenerado {
		logger.Warn("MULTIPARTY_SECRET no está definido: se usa uno aleatorio y las sesiones se pierden al reiniciar")
	}

	op := web.Opciones{
		Resolver: resolver.New(cfg.Pais),
		Secreto:  cfg.Secreto,
	}
	if cfg.GoogleHabilitado() || cfg.SpotifyHabilitado() {
		caja, err := cifrado.DesdeBase64(cfg.ClaveTokens)
		if err != nil {
			return errors.Join(errors.New("MULTIPARTY_CLAVE_TOKENS inválida"), err)
		}
		op.Caja = caja
	}
	if cfg.GoogleHabilitado() {
		op.Google = &oauth2.Config{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			Endpoint:     endpoints.Google,
			RedirectURL:  cfg.BaseURL + "/cuentas/youtube/callback",
			Scopes:       []string{youtube.Scope},
		}
		logger.Info("YouTube Music habilitado", "redirect", op.Google.RedirectURL)
	} else {
		logger.Info("YouTube Music deshabilitado: falta GOOGLE_CLIENT_ID")
	}
	if cfg.SpotifyHabilitado() {
		op.Spotify = &oauth2.Config{
			ClientID:     cfg.SpotifyClientID,
			ClientSecret: cfg.SpotifyClientSecret,
			Endpoint:     endpoints.Spotify,
			RedirectURL:  cfg.BaseURL + "/cuentas/spotify/callback",
			Scopes:       spotify.Scopes,
		}
		logger.Info("Spotify habilitado", "redirect", op.Spotify.RedirectURL)
	} else {
		logger.Info("Spotify deshabilitado: falta SPOTIFY_CLIENT_ID")
	}

	app, err := web.New(database, logger, op)
	if err != nil {
		return err
	}

	// Escuchas de Spotify, pre-emparejar y refrescar datos de YouTube.
	tareas.New(app.Store(), app.Conexiones(), logger).Correr(ctx)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("servidor escuchando", "addr", cfg.Addr, "db", cfg.DatabasePath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	logger.Info("apagando servidor")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
