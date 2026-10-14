package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/tehbooom/project_name/internal/app"
	"github.com/tehbooom/project_name/internal/auth"
	"github.com/tehbooom/project_name/internal/otl"
	"github.com/tehbooom/project_name/internal/wordstore"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	if err := run(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

func run() (err error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	otelShutdown, err := otl.SetupOTelSDK(ctx)
	if err != nil {
		return err
	}

	defer func() {
		shutdownErr := otelShutdown(context.Background())
		if shutdownErr != nil {
			log.Printf("shut down OpenTelemetry: %v", shutdownErr)
		}
		err = errors.Join(err, shutdownErr)
	}()

	startupCtx, startupSpan := otel.Tracer("github.com/tehbooom/project_name").Start(
		ctx,
		"application.startup",
	)
	a := app.App{}
	authorizer, err := auth.NewFromEnvironment(startupCtx)
	if err != nil {
		recordStartupError(startupCtx, startupSpan, "authentication", err)
		startupSpan.End()
		return err
	}
	startupSpan.AddEvent("authentication initialized")

	pathName := os.Getenv("WORDS_DIR")
	if pathName == "" {
		pathName = "words"
	}
	words, err := wordstore.NewFromEnvironment(startupCtx, pathName)
	if err != nil {
		recordStartupError(startupCtx, startupSpan, "word_store", err)
		startupSpan.End()
		return err
	}
	startupSpan.AddEvent("word store initialized")
	defer func() {
		closeCtx := context.Background()
		slog.InfoContext(closeCtx, "closing word store")
		err = errors.Join(err, words.Close(closeCtx))
	}()

	portNumber := os.Getenv("LISTENING_PORT")
	if portNumber == "" {
		portNumber = os.Getenv("PORT")
		if portNumber == "" {
			portNumber = "8080"
		}
	}

	err = a.Initialize(words, app.WithAuthorizer(authorizer))
	if err != nil {
		recordStartupError(startupCtx, startupSpan, "application", err)
		startupSpan.End()
		return err
	}
	startupSpan.SetAttributes(
		attribute.String("wordstore.driver", configuredValue(wordstore.DriverEnvironment, "memory")),
		attribute.String("auth.drivers", configuredValue(auth.DriversEnvironment, "none")),
	)
	startupSpan.SetStatus(codes.Ok, "initialized")
	slog.InfoContext(startupCtx, "application initialized",
		"wordstore_driver", configuredValue(wordstore.DriverEnvironment, "memory"),
		"auth_drivers", configuredValue(auth.DriversEnvironment, "none"),
	)
	startupSpan.End()

	return a.Run(ctx, ":"+portNumber)
}

func recordStartupError(ctx context.Context, span trace.Span, component string, err error) {
	span.RecordError(errors.New(component + " initialization failed"))
	span.SetAttributes(attribute.String("error.type", component+"_initialization"))
	span.SetStatus(codes.Error, component+"_initialization")
	slog.ErrorContext(ctx, "application startup failed", "component", component, "error", err)
}

func configuredValue(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
