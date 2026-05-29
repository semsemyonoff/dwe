package widgets

import (
	"errors"
	"strings"
	"testing"

	huh "charm.land/huh/v2"
)

func TestRunConfirm_True(t *testing.T) {
	orig := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = orig })

	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		return true, nil
	}

	result, err := RunConfirm("Proceed?", "Yes", "No")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected true")
	}
}

func TestRunConfirm_False(t *testing.T) {
	orig := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = orig })

	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		return false, nil
	}

	result, err := RunConfirm("Proceed?", "Yes", "No")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected false")
	}
}

func TestRunConfirm_ErrCancelled_OnUserAborted(t *testing.T) {
	orig := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = orig })

	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		return false, huh.ErrUserAborted
	}

	_, err := RunConfirm("Proceed?", "Yes", "No")
	if !errors.Is(err, ErrCancelled) {
		t.Errorf("expected ErrCancelled, got %v", err)
	}
}

func TestRunConfirm_OtherError_Propagated(t *testing.T) {
	orig := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = orig })

	sentinel := errors.New("form error")
	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		return false, sentinel
	}

	_, err := RunConfirm("Proceed?", "Yes", "No")
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

func TestRunConfirm_ReceivesCorrectArgs(t *testing.T) {
	orig := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = orig })

	var gotTitle, gotAff, gotNeg string
	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		gotTitle = title
		gotAff = affirmative
		gotNeg = negative
		return true, nil
	}

	_, _ = RunConfirm("Delete everything?", "Yes, delete", "Cancel")
	if gotTitle != "Delete everything?" {
		t.Errorf("expected title %q, got %q", "Delete everything?", gotTitle)
	}
	if gotAff != "Yes, delete" {
		t.Errorf("expected affirmative %q, got %q", "Yes, delete", gotAff)
	}
	if gotNeg != "Cancel" {
		t.Errorf("expected negative %q, got %q", "Cancel", gotNeg)
	}
}

func TestRenderParamSummary(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		want   string
	}{
		{
			name:   "empty returns empty string",
			values: map[string]string{},
			want:   "",
		},
		{
			name:   "nil returns empty string",
			values: nil,
			want:   "",
		},
		{
			name:   "single key",
			values: map[string]string{"task": "cleanup"},
			want:   "task = cleanup",
		},
		{
			name:   "sorted by key with alignment",
			values: map[string]string{"task": "cleanup", "mode": "safe", "verbose": "true"},
			want:   "mode    = safe\ntask    = cleanup\nverbose = true",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderParamSummary(tt.values)
			if got != tt.want {
				t.Errorf("renderParamSummary() =\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

func TestConfirmRun_EmptyValues_FallsBackToRunConfirm(t *testing.T) {
	orig := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = orig })

	origRun := runConfirmRunFormFn
	t.Cleanup(func() { runConfirmRunFormFn = origRun })

	var fallbackCalled bool
	var gotTitle, gotAff, gotNeg string
	runConfirmFormFn = func(title, aff, neg string) (bool, error) {
		fallbackCalled = true
		gotTitle, gotAff, gotNeg = title, aff, neg
		return true, nil
	}
	runConfirmRunFormFn = func(title, summary string) (bool, error) {
		t.Fatal("runConfirmRunFormFn must not be called when values empty")
		return false, nil
	}

	ok, err := ConfirmRun("Proceed?", map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected true")
	}
	if !fallbackCalled {
		t.Error("expected fallback RunConfirm path to be used")
	}
	if gotTitle != "Proceed?" || gotAff != "Yes" || gotNeg != "No" {
		t.Errorf("unexpected fallback args: title=%q aff=%q neg=%q", gotTitle, gotAff, gotNeg)
	}
}

func TestConfirmRun_SummaryWithSortedKeys(t *testing.T) {
	orig := runConfirmRunFormFn
	t.Cleanup(func() { runConfirmRunFormFn = orig })

	var gotTitle, gotSummary string
	runConfirmRunFormFn = func(title, summary string) (bool, error) {
		gotTitle, gotSummary = title, summary
		return true, nil
	}

	values := map[string]string{"b": "two", "a": "one"}
	ok, err := ConfirmRun("Run cleanup?", values)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected true")
	}
	if gotTitle != "Run cleanup?" {
		t.Errorf("title not passed through verbatim: %q", gotTitle)
	}
	// keys must be sorted alphabetically
	idxA := strings.Index(gotSummary, "a ")
	idxB := strings.Index(gotSummary, "b ")
	if idxA < 0 || idxB < 0 || idxA > idxB {
		t.Errorf("expected sorted keys, got summary:\n%s", gotSummary)
	}
}

func TestConfirmRun_Yes(t *testing.T) {
	orig := runConfirmRunFormFn
	t.Cleanup(func() { runConfirmRunFormFn = orig })

	runConfirmRunFormFn = func(title, summary string) (bool, error) {
		return true, nil
	}

	ok, err := ConfirmRun("Proceed?", map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected true")
	}
}

func TestConfirmRun_No(t *testing.T) {
	orig := runConfirmRunFormFn
	t.Cleanup(func() { runConfirmRunFormFn = orig })

	runConfirmRunFormFn = func(title, summary string) (bool, error) {
		return false, nil
	}

	ok, err := ConfirmRun("Proceed?", map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected false")
	}
}

func TestConfirmRun_Cancel_ReturnsErrCancelled(t *testing.T) {
	orig := runConfirmRunFormFn
	t.Cleanup(func() { runConfirmRunFormFn = orig })

	runConfirmRunFormFn = func(title, summary string) (bool, error) {
		return false, huh.ErrUserAborted
	}

	ok, err := ConfirmRun("Proceed?", map[string]string{"k": "v"})
	if !errors.Is(err, ErrCancelled) {
		t.Errorf("expected ErrCancelled, got %v", err)
	}
	if ok {
		t.Error("expected false on cancel")
	}
}

func TestConfirmRun_OtherError_Propagated(t *testing.T) {
	orig := runConfirmRunFormFn
	t.Cleanup(func() { runConfirmRunFormFn = orig })

	sentinel := errors.New("form error")
	runConfirmRunFormFn = func(title, summary string) (bool, error) {
		return false, sentinel
	}

	_, err := ConfirmRun("Proceed?", map[string]string{"k": "v"})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel, got %v", err)
	}
}

func TestConfirmRun_TitlePassedVerbatim(t *testing.T) {
	orig := runConfirmRunFormFn
	t.Cleanup(func() { runConfirmRunFormFn = orig })

	// Caller is responsible for template expansion — ui must pass through verbatim.
	const literal = "Run ${param.task}?"
	var gotTitle string
	runConfirmRunFormFn = func(title, summary string) (bool, error) {
		gotTitle = title
		return true, nil
	}

	_, _ = ConfirmRun(literal, map[string]string{"task": "cleanup"})
	if gotTitle != literal {
		t.Errorf("expected title %q passed through verbatim, got %q", literal, gotTitle)
	}
}
