package migrations

import (
	"strings"
	"testing"
)

func TestAllReturnsContiguousImmutablePrivacySafeMigrations(t *testing.T) {
	t.Parallel()
	migrations, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 1 || migrations[0].Version != 1 || migrations[0].Name != "initialize_server" || len(migrations[0].Checksum) != 64 {
		t.Fatalf("All() = %#v", migrations)
	}
	for _, prohibited := range []string{
		"prompt_text", "response_text", "source_code", "file_path", "project_name",
		"repository_name", "branch_name", "command_text", "provider_credential",
	} {
		if strings.Contains(strings.ToLower(migrations[0].SQL), prohibited) {
			t.Fatalf("migration contains prohibited field %q", prohibited)
		}
	}
	migrations[0].SQL = "mutated"
	again, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if again[0].SQL == "mutated" {
		t.Fatal("All() returned mutable shared migration state")
	}
}
