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
	if len(migrations) != 2 || migrations[0].Version != 1 || migrations[0].Name != "initialize_server" || migrations[1].Version != 2 || migrations[1].Name != "private_groups" {
		t.Fatalf("All() = %#v", migrations)
	}
	for _, migration := range migrations {
		if len(migration.Checksum) != 64 {
			t.Fatalf("migration %d checksum = %q", migration.Version, migration.Checksum)
		}
	}
	for _, prohibited := range []string{
		"prompt_text", "response_text", "source_code", "file_path", "project_name",
		"repository_name", "branch_name", "command_text", "provider_credential",
	} {
		for _, migration := range migrations {
			if strings.Contains(strings.ToLower(migration.SQL), prohibited) {
				t.Fatalf("migration %d contains prohibited field %q", migration.Version, prohibited)
			}
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
