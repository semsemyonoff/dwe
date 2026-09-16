// External test package: usercommands imports packcommon, so building a real
// registry from an internal test would be an import cycle.
package packcommon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/templates/packcommon"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// TestServiceCommandGroups_MagentoShape loads the magento layout through the
// production loader: no services.yml, so the registry holds a meta-less
// `services` ancestor above `services.magento` and its 17 leaf groups, plus a
// sibling hub. The collapse must land on `services.magento` — never on
// `services`, which would carry no description and swallow the sibling.
func TestServiceCommandGroups_MagentoShape(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("services/magento.yml", `
group:
  description: Commands for the Magento application service
commands:
  shell:
    type: service_exec
    service: app-magento
    cmd: bash
`)
	for i := range 17 {
		write(fmt.Sprintf("services/magento/leaf%02d.yml", i), fmt.Sprintf(`
group:
  description: Leaf %02d
commands:
  run:
    type: service_exec
    service: app-magento
    cmd: bin/magento leaf%02d
`, i, i))
	}
	write("services/search.yml", `
group:
  description: Search service
commands:
  reindex:
    type: service_exec
    service: search
    cmd: reindex
`)

	reg, err := usercommands.LoadRegistry(dir)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if !hasChild(reg.Groups(), "services") {
		t.Fatal("fixture no longer materializes the synthetic services ancestor")
	}
	cmds, groups := usercommands.CommandIndex(reg, i18n.NopTranslator{}, "")

	d := packcommon.TemplateData{
		Resolved:      "magento",
		ServiceCfg:    config.ServiceConfig{Container: "app-magento"},
		Commands:      cmds,
		CommandGroups: groups,
	}
	got := d.ServiceCommandGroups()
	if len(got) != 1 || got[0].ID != "services.magento" {
		t.Fatalf("ServiceCommandGroups() = %+v, want exactly services.magento", got)
	}
	if got[0].Description == "" {
		t.Error("services.magento description is empty")
	}
	if got[0].Count != 18 {
		t.Errorf("services.magento Count = %d, want 18", got[0].Count)
	}
	for _, c := range d.ServiceCommands() {
		if !strings.HasPrefix(c.ID, "services.magento.") {
			t.Errorf("ServiceCommands() includes foreign command %q", c.ID)
		}
	}
}

func hasChild(gn *usercommands.GroupNode, id string) bool {
	for _, c := range gn.Children {
		if c.ID == id {
			return true
		}
	}
	return false
}
