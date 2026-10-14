package wordstore

import (
	"context"
	"testing"

	"github.com/tehbooom/project_name/internal/model"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestInstrumentedStoreEmitsOperationSpan(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
	})

	words := model.NewWordList()
	if _, err := words.CreateProposal(model.CategoryNouns, "gopher"); err != nil {
		t.Fatal(err)
	}
	noun, err := words.ListWords(model.CategoryNouns, model.StatusProposed, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := words.ApproveWord(noun.Words[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := words.CreateProposal(model.CategoryAdjectives, "brave"); err != nil {
		t.Fatal(err)
	}
	adjective, err := words.ListWords(model.CategoryAdjectives, model.StatusProposed, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := words.ApproveWord(adjective.Words[0].ID); err != nil {
		t.Fatal(err)
	}

	store, err := instrumentStore(NewMemory(words), "memory")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetWords(context.Background(), model.NameConstraints{}); err != nil {
		t.Fatal(err)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	if spans[0].Name() != "wordstore.get_words" {
		t.Fatalf("span name = %q, want wordstore.get_words", spans[0].Name())
	}
	attributes := spans[0].Attributes()
	foundDriver := false
	for _, value := range attributes {
		if string(value.Key) == "wordstore.driver" && value.Value.AsString() == "memory" {
			foundDriver = true
		}
	}
	if !foundDriver {
		t.Fatal("wordstore.driver=memory attribute was not recorded")
	}
}
