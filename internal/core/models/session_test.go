package models

import (
	"testing"
	"time"
)

func TestSessionValidation(t *testing.T) {
	tests := []struct {
		name    string
		session Session
		wantErr bool
	}{
		{
			name: "valid session",
			session: Session{
				SessionID:   "abc-123",
				ProjectPath: "/Users/neil/xuku/invoice",
				Summary:     "Test session",
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			},
			wantErr: false,
		},
		{
			name: "missing session ID",
			session: Session{
				ProjectPath: "/Users/neil/xuku/invoice",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.session.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
