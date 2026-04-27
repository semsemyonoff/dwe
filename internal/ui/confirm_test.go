package ui

import (
	"errors"
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
