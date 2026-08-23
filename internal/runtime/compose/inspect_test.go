package compose_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/runtime/compose/runtimetest"
)

// TestAServiceRunningTheOrdinaryCheckoutIsReported is the failure this package is
// least able to see on its own.
//
// A repository's container_path is the path the *agent's* Compose files mount it
// at. The application's Compose files are a different set and may use another
// path, and Compose replaces a mount only when the target matches — so a
// mismatch leaves the base file's own mount in place and the services run the
// user's ordinary checkout. Everything Feat generated is correct, every record it
// keeps is correct, and the user's change simply has no effect.
func TestAServiceRunningTheOrdinaryCheckoutIsReported(t *testing.T) {
	docker := runtimetest.New().
		Answer("inspect --type container --format {{json .Mounts}} c0ffee",
			`[{"Type":"bind","Source":"/repos/app/api","Destination":"/app","RW":true},
			  {"Type":"bind","Source":"/worktrees/api","Destination":"/srv/api","RW":true}]`)
	services, _ := arrange(t, docker)
	ctx := context.Background()

	state, err := services.Start(ctx)
	if err != nil {
		t.Fatalf("starting: %v", err)
	}
	report, err := services.Inspect(ctx, state)
	if err != nil {
		t.Fatalf("inspecting: %v", err)
	}

	if len(report.Notes) != 1 {
		t.Fatalf("%d notes were produced, want 1: %v", len(report.Notes), report.Notes)
	}
	note := report.Notes[0]
	for _, required := range []string{"api", "/repos/app/api", "container_path"} {
		if !strings.Contains(note, required) {
			t.Errorf("the note does not name %q: %s", required, note)
		}
	}
}

// TestAServiceWithOnlyItsWorktreeIsQuiet keeps the check from becoming one a
// user learns to ignore.
func TestAServiceWithOnlyItsWorktreeIsQuiet(t *testing.T) {
	docker := runtimetest.New().
		Answer("inspect --type container --format {{json .Mounts}} c0ffee",
			`[{"Type":"bind","Source":"/worktrees/api","Destination":"/srv/api","RW":true},
			  {"Type":"volume","Name":"pgdata","Destination":"/var/lib/postgresql/data","RW":true}]`)
	services, _ := arrange(t, docker)
	ctx := context.Background()

	state, err := services.Start(ctx)
	if err != nil {
		t.Fatalf("starting: %v", err)
	}
	report, err := services.Inspect(ctx, state)
	if err != nil {
		t.Fatalf("inspecting: %v", err)
	}
	if len(report.Notes) != 0 {
		t.Fatalf("a correct runtime produced notes: %v", report.Notes)
	}
	if len(report.Mounts) != 2 {
		t.Fatalf("%d mounts were reported, want 2", len(report.Mounts))
	}
	if report.Mounts[0].Service != "api" {
		t.Errorf("a mount is not attributed to the service that reported it: %+v", report.Mounts[0])
	}
}

// TestACheckoutIsRecognisedThroughTheContainerRuntimesOwnPrefix covers what
// Docker Desktop reports.
//
// It shares the host filesystem through its own virtual machine, so a bind
// source comes back as /host_mnt/repos/app/api. A check that missed it would be
// silent on the platform the dogfood runs on.
func TestACheckoutIsRecognisedThroughTheContainerRuntimesOwnPrefix(t *testing.T) {
	docker := runtimetest.New().
		Answer("inspect --type container --format {{json .Mounts}} c0ffee",
			`[{"Type":"bind","Source":"/host_mnt/repos/app/api/src","Destination":"/app/src","RW":true}]`)
	services, _ := arrange(t, docker)
	ctx := context.Background()

	state, err := services.Start(ctx)
	if err != nil {
		t.Fatalf("starting: %v", err)
	}
	report, err := services.Inspect(ctx, state)
	if err != nil {
		t.Fatalf("inspecting: %v", err)
	}
	if len(report.Notes) != 1 {
		t.Fatalf("a checkout mounted through the runtime's own prefix was not recognised: %v", report.Notes)
	}
}

// TestAnUnrelatedPathThatMerelyLooksLikeOneIsNotReported keeps the check
// trustworthy: a note about a repository the user does not have is a note they
// will learn to skip.
func TestAnUnrelatedPathThatMerelyLooksLikeOneIsNotReported(t *testing.T) {
	docker := runtimetest.New().
		Answer("inspect --type container --format {{json .Mounts}} c0ffee",
			`[{"Type":"bind","Source":"/elsewhere/repos/app/api","Destination":"/app","RW":true}]`)
	services, _ := arrange(t, docker)
	ctx := context.Background()

	state, err := services.Start(ctx)
	if err != nil {
		t.Fatalf("starting: %v", err)
	}
	report, err := services.Inspect(ctx, state)
	if err != nil {
		t.Fatalf("inspecting: %v", err)
	}
	if len(report.Notes) != 0 {
		t.Fatalf("an unrelated path was reported as a checkout: %v", report.Notes)
	}
}
