package service

import (
	"context"
	"errors"
	"testing"

	"github.com/yourname/sitepulse/internal/domain"
	"github.com/yourname/sitepulse/internal/validation"
)

func validCreateInput() CreateMonitorInput {
	return CreateMonitorInput{
		Name:            "My Website",
		URL:             "https://example.com",
		IntervalSeconds: 60,
		TimeoutSeconds:  10,
	}
}

func TestMonitorService_Create(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(in *CreateMonitorInput)
		wantErr error
	}{
		{
			name:   "valid input succeeds",
			modify: func(in *CreateMonitorInput) {},
		},
		{
			name:    "invalid URL is rejected",
			modify:  func(in *CreateMonitorInput) { in.URL = "not-a-url" },
			wantErr: validation.ErrInvalidURL,
		},
		{
			name:    "javascript scheme is rejected",
			modify:  func(in *CreateMonitorInput) { in.URL = "javascript:alert(1)" },
			wantErr: validation.ErrInvalidURL,
		},
		{
			name:    "empty name is rejected",
			modify:  func(in *CreateMonitorInput) { in.Name = "" },
			wantErr: validation.ErrInvalidName,
		},
		{
			name:    "interval below minimum is rejected",
			modify:  func(in *CreateMonitorInput) { in.IntervalSeconds = 5 },
			wantErr: validation.ErrIntervalTooShort,
		},
		{
			name: "timeout greater than or equal to interval is rejected",
			modify: func(in *CreateMonitorInput) {
				in.IntervalSeconds = 10
				in.TimeoutSeconds = 10
			},
			wantErr: validation.ErrTimeoutExceedsInterval,
		},
		{
			name:    "invalid type is rejected",
			modify:  func(in *CreateMonitorInput) { in.Type = "ftp" },
			wantErr: ErrInvalidMonitorType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeMonitorRepo()
			svc := NewMonitorService(repo, newFakeCheckRepo(), newFakeIncidentRepo())
			ctx := context.Background()

			in := validCreateInput()
			tt.modify(&in)

			m, err := svc.Create(ctx, 1, in)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m.UserID != 1 {
				t.Errorf("expected UserID 1, got %d", m.UserID)
			}
			if !m.Active {
				t.Error("newly created monitor should default to active")
			}
			if m.Type != domain.MonitorTypeHTTP {
				t.Errorf("expected default type %q, got %q", domain.MonitorTypeHTTP, m.Type)
			}
			if m.ExpectedStatusCode != 200 {
				t.Errorf("expected default expected_status_code 200, got %d", m.ExpectedStatusCode)
			}
		})
	}
}

func TestMonitorService_List(t *testing.T) {
	repo := newFakeMonitorRepo()
	svc := NewMonitorService(repo, newFakeCheckRepo(), newFakeIncidentRepo())
	ctx := context.Background()

	if _, err := svc.Create(ctx, 1, validCreateInput()); err != nil {
		t.Fatalf("setup create failed: %v", err)
	}
	if _, err := svc.Create(ctx, 1, validCreateInput()); err != nil {
		t.Fatalf("setup create failed: %v", err)
	}
	if _, err := svc.Create(ctx, 2, validCreateInput()); err != nil {
		t.Fatalf("setup create failed: %v", err)
	}

	monitors, err := svc.List(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(monitors) != 2 {
		t.Fatalf("expected 2 monitors for user 1, got %d — List must not leak other users' monitors", len(monitors))
	}
	for _, m := range monitors {
		if m.UserID != 1 {
			t.Errorf("List(1) returned a monitor owned by user %d", m.UserID)
		}
	}
}

func TestMonitorService_OwnershipEnforcement(t *testing.T) {
	ctx := context.Background()

	setup := func(t *testing.T) (*MonitorService, int64) {
		repo := newFakeMonitorRepo()
		svc := NewMonitorService(repo, newFakeCheckRepo(), newFakeIncidentRepo())
		m, err := svc.Create(ctx, 1, validCreateInput()) // owned by user 1
		if err != nil {
			t.Fatalf("setup create failed: %v", err)
		}
		return svc, m.ID
	}

	t.Run("owner can Get their own monitor", func(t *testing.T) {
		svc, id := setup(t)
		if _, err := svc.Get(ctx, 1, domain.RoleViewer, id); err != nil {
			t.Fatalf("owner should be able to Get, got error: %v", err)
		}
	})

	t.Run("non-owner cannot Get someone else's monitor", func(t *testing.T) {
		svc, id := setup(t)
		_, err := svc.Get(ctx, 2, domain.RoleOperator, id)
		if !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("admin can Get any monitor regardless of ownership", func(t *testing.T) {
		svc, id := setup(t)
		if _, err := svc.Get(ctx, 999, domain.RoleAdmin, id); err != nil {
			t.Fatalf("admin should be able to Get any monitor, got error: %v", err)
		}
	})

	t.Run("unknown monitor id returns NotFound, not Forbidden", func(t *testing.T) {
		svc, _ := setup(t)
		_, err := svc.Get(ctx, 1, domain.RoleViewer, 999999)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("non-owner cannot Update", func(t *testing.T) {
		svc, id := setup(t)
		in := UpdateMonitorInput{Name: "Hijacked", URL: "https://evil.example.com", IntervalSeconds: 60, TimeoutSeconds: 10}
		_, err := svc.Update(ctx, 2, domain.RoleOperator, id, in)
		if !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("non-owner cannot Delete", func(t *testing.T) {
		svc, id := setup(t)
		err := svc.Delete(ctx, 2, domain.RoleOperator, id)
		if !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("owner can Update and the change persists", func(t *testing.T) {
		svc, id := setup(t)
		in := UpdateMonitorInput{Name: "Renamed", URL: "https://renamed.example.com", IntervalSeconds: 120, TimeoutSeconds: 10, Active: true}
		updated, err := svc.Update(ctx, 1, domain.RoleViewer, id, in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if updated.Name != "Renamed" {
			t.Errorf("expected name %q, got %q", "Renamed", updated.Name)
		}

		refetched, err := svc.Get(ctx, 1, domain.RoleViewer, id)
		if err != nil {
			t.Fatalf("re-fetch failed: %v", err)
		}
		if refetched.Name != "Renamed" {
			t.Error("update did not actually persist in the repository")
		}
	})

	t.Run("owner can Delete", func(t *testing.T) {
		svc, id := setup(t)
		if err := svc.Delete(ctx, 1, domain.RoleViewer, id); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, err := svc.Get(ctx, 1, domain.RoleViewer, id)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("expected monitor to be gone after delete, got %v", err)
		}
	})
}
