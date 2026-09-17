package process

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFakeProcessRunnerRecordsAndInjectsFailure(t *testing.T) {
	t.Parallel()
	boom := errors.New("injected")
	fake := &FakeRunner{
		Responses: map[string]Result{
			"herdr version": {ExitCode: 0, Stdout: []byte("0.8.2\n")},
		},
		Failures: map[string]error{
			"git status": boom,
		},
	}

	ok, err := fake.Run(context.Background(), Spec{Name: "herdr", Args: []string{"version"}})
	if err != nil {
		t.Fatalf("herdr version: %v", err)
	}
	if ok.ExitCode != 0 || string(ok.Stdout) != "0.8.2\n" {
		t.Fatalf("unexpected result %#v", ok)
	}

	_, err = fake.Run(context.Background(), Spec{Name: "git", Args: []string{"status"}})
	if !errors.Is(err, boom) {
		t.Fatalf("git status err = %v, want injected", err)
	}

	if len(fake.Calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(fake.Calls))
	}
	if fake.Calls[0].Name != "herdr" || fake.Calls[1].Name != "git" {
		t.Fatalf("calls = %#v", fake.Calls)
	}
}

func TestFakeProcessRunnerCopiesSpecSlices(t *testing.T) {
	t.Parallel()
	args := []string{"status"}
	fake := &FakeRunner{}
	_, err := fake.Run(context.Background(), Spec{Name: "git", Args: args})
	if err != nil {
		t.Fatal(err)
	}
	args[0] = "mutated"
	if fake.Calls[0].Args[0] != "status" {
		t.Fatalf("recorded args mutated: %#v", fake.Calls[0].Args)
	}
}

func TestFakeProcessRunnerHonorsCanceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := &FakeRunner{}
	got, err := fake.Run(ctx, Spec{Name: "herdr"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
	if got.ExitCode != 0 || len(got.Stdout) != 0 || len(got.Stderr) != 0 {
		t.Fatalf("result on cancel = %#v", got)
	}
}

func TestFakeProcessRunnerHonorsDeadlineExceeded(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	fake := &FakeRunner{Default: Result{ExitCode: -1}}
	got, err := fake.Run(ctx, Spec{Name: "herdr"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if got.ExitCode != 0 || len(got.Stdout) != 0 || len(got.Stderr) != 0 {
		t.Fatalf("context kill must not return a result: %#v", got)
	}
}

func TestFakeProcessRunnerHandler(t *testing.T) {
	t.Parallel()
	fake := &FakeRunner{
		Handler: func(_ context.Context, spec Spec) (Result, error) {
			return Result{ExitCode: 3, Stdout: []byte(spec.Name)}, nil
		},
		Failures: map[string]error{"herdr": errors.New("ignored")},
	}
	got, err := fake.Run(context.Background(), Spec{Name: "herdr"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ExitCode != 3 || string(got.Stdout) != "herdr" {
		t.Fatalf("got %#v", got)
	}
}

func TestFakeProcessRunnerDefault(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	fake := &FakeRunner{Default: Result{ExitCode: 7, Stderr: []byte("nope")}}
	got, err := fake.Run(ctx, Spec{Name: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ExitCode != 7 {
		t.Fatalf("exit = %d", got.ExitCode)
	}
}
