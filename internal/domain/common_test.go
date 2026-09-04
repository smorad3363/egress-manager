package domain

import "testing"

func TestIDValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		id      ID
		wantErr bool
	}{
		{name: "slug", id: "route-eu-1"},
		{name: "underscore", id: "route_eu_1"},
		{name: "empty", id: "", wantErr: true},
		{name: "uppercase", id: "Route-1", wantErr: true},
		{name: "space", id: "route 1", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.id.Validate("id")
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
