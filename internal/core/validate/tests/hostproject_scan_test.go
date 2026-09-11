package tests

import (
	"reflect"
	"testing"
)

type scanCase struct {
	name string
	text string
	want []hit
}

func runScanCases(t *testing.T, cases []scanCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scanShellText(tc.text)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("scanShellText(%q)\n got: %#v\nwant: %#v", tc.text, got, tc.want)
			}
		})
	}
}

func TestScanShellTextHits(t *testing.T) {
	runScanCases(t, []scanCase{
		{
			name: "composite name via one-hop variable",
			text: "PROJECT=\"${PROJECT_PREFIX:-dwe}-${PROJECT_NAME:-myproj}\"\n" +
				"docker compose -p \"$PROJECT\" exec db psql\n",
			want: []hit{{Line: 2, Value: "${PROJECT_PREFIX:-dwe}-${PROJECT_NAME:-myproj}"}},
		},
		{
			name: "inline composite",
			text: `docker compose -p "${A:-dwe}-x" up -d`,
			want: []hit{{Line: 1, Value: "${A:-dwe}-x"}},
		},
		{
			name: "long flag with equals",
			text: `docker compose --project-name="${A}-x" up`,
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{
			name: "long flag with separate value",
			text: `docker compose --project-name "${A}-x" up`,
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{
			name: "short flag with equals",
			text: `docker compose -p="${A}-x" up`,
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{
			name: "short flag glued to value",
			text: `docker compose -p"${A}-x" up`,
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{
			name: "value-taking globals before -p are skipped",
			text: `docker compose -f a.yml --env-file .env -p "${A}-x" up`,
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{
			name: "docker-compose v1",
			text: `docker-compose -p "${A}-x" up`,
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{
			name: "docker by absolute path",
			text: `/usr/local/bin/docker compose -p "${A}-x" up`,
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{
			name: "docker binary from an expansion",
			text: `${DOCKER:-docker} compose -p "${A}-x" up`,
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{
			name: "exported assignment",
			text: `export COMPOSE_PROJECT_NAME="${A}-x"`,
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{
			name: "prefix assignment",
			text: `COMPOSE_PROJECT_NAME="${A}-x" docker compose up`,
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{
			name: "continued line reports its first line",
			text: "echo start\n" +
				"docker compose \\\n" +
				"  -f a.yml \\\n" +
				"  -p \"${A}-x\" \\\n" +
				"  up\n" +
				"echo done\n",
			want: []hit{{Line: 2, Value: "${A}-x"}},
		},
	})
}

func TestScanShellTextBoundaryHits(t *testing.T) {
	runScanCases(t, []scanCase{
		{
			name: "suffixed variable is not a reference",
			text: `docker compose -p "${COMPOSE_PROJECT_NAME_OLD}-x" up`,
			want: []hit{{Line: 1, Value: "${COMPOSE_PROJECT_NAME_OLD}-x"}},
		},
		{
			name: "bare suffixed variable without assignment",
			text: `docker compose -p "${COMPOSE_PROJECT_NAME_OLD}" up`,
			want: nil,
		},
		{
			name: "after background separator",
			text: `sleep 1 & docker compose -p "${A}-x" up`,
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{
			name: "second line of a multi-line cmd",
			text: "cd app\ndocker compose -p \"${A}-x\" up\n",
			want: []hit{{Line: 2, Value: "${A}-x"}},
		},
		{
			name: "declare assignment one hop",
			text: "declare PROJECT=\"${A}-x\"\ndocker compose -p \"$PROJECT\" up\n",
			want: []hit{{Line: 2, Value: "${A}-x"}},
		},
		{
			name: "COMPOSE_PROJECT_NAME assigned from a composite variable",
			text: "P=\"${A}-x\"\nexport COMPOSE_PROJECT_NAME=\"$P\"\n",
			want: []hit{{Line: 2, Value: "${A}-x"}},
		},
	})
}

func TestScanShellTextNoHits(t *testing.T) {
	runScanCases(t, []scanCase{
		{name: "braced reference", text: `docker compose -p "${COMPOSE_PROJECT_NAME}" up`},
		{name: "bare reference", text: `docker compose -p "$COMPOSE_PROJECT_NAME" up`},
		{name: "reference with fallback", text: `docker compose -p "${COMPOSE_PROJECT_NAME:-dwe-x}" up`},
		{
			name: "fixed motivating shape",
			text: "PROJECT=\"${COMPOSE_PROJECT_NAME:-${PROJECT_PREFIX:-dwe}-${PROJECT_NAME:-myproj}}\"\n" +
				"docker compose -p \"$PROJECT\" exec db psql\n",
		},
		{
			name: "variable assigned from reference with fallback",
			text: "PROJECT=\"${COMPOSE_PROJECT_NAME:-dwe-x}\"\ndocker compose -p \"$PROJECT\" up\n",
		},
		{name: "positional", text: `docker compose -p "$1" up`},
		{name: "literal", text: `docker compose -p dwe-myproj up`},
		{name: "single-quoted literal", text: `docker compose -p '${A}-x' up`},
		{name: "variable without assignment", text: `docker compose -p "$X" up`},
		{name: "variable assigned from another variable", text: "X=\"$Y\"\ndocker compose -p \"$X\" up\n"},
		{
			name: "override idiom",
			text: "PROJECT=\"${OVERRIDE:-$COMPOSE_PROJECT_NAME}\"\ndocker compose -p \"$PROJECT\" up\n",
		},
		{name: "positional with reference fallback", text: `docker compose -p "${1:-$COMPOSE_PROJECT_NAME}" up`},
		{name: "positional with literal fallback", text: `docker compose -p "${1:-dwe-x}" up`},
		{
			name: "disagreeing assignments",
			text: "declare -r PROJECT=\"$COMPOSE_PROJECT_NAME\"\n" +
				"PROJECT=\"${A}-x\"\n" +
				"docker compose -p \"$PROJECT\" up\n",
		},
		{name: "command substitution", text: "PROJECT=$(get_project)\ndocker compose -p \"$PROJECT\" up\n"},
		{name: "backtick substitution", text: "PROJECT=`cmd`\ndocker compose -p \"$PROJECT\" up\n"},
		{
			name: "quoted substitution with a pipe",
			text: "PROJECT=\"$(docker compose ls -q | head -1)\"\ndocker compose -p \"$PROJECT\" up\n",
		},
		{name: "inline substitution", text: `docker compose -p "$(get_project)" up`},
		{name: "read-assigned variable", text: "read -r PROJECT\ndocker compose -p \"$PROJECT\" up\n"},
		{name: "mkdir -p", text: `mkdir -p "${A}-x"`},
		{name: "-p after the subcommand", text: `docker compose exec db psql -p "${A}-x"`},
		{name: "-p after the subcommand, literal port", text: `docker compose exec db psql -p 5432`},
		{name: "docker global flag before compose", text: `docker --context x compose -p "${A}-x" up`},
		{name: "commented-out line", text: "# docker compose -p \"${A}-x\" up\necho ok\n"},
		{name: "trailing comment", text: `echo ok # docker compose -p "${A}-x" up`},
		{name: "-p inside a quoted argument", text: `echo "docker compose -p ${A}-x up"`},
		{name: "-p inside a single-quoted sh -c payload", text: `sh -c 'docker compose -p "${A}-x" up'`},
		{
			name: "-p inside a here-doc body",
			text: "cat <<EOF > run.sh\ndocker compose -p \"${A}-x\" up\nEOF\necho ok\n",
		},
		{name: "reference assignment", text: `export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-dwe-x}"`},
		{name: "bare export", text: `export COMPOSE_PROJECT_NAME`},
		{name: "empty text", text: ""},
	})
}

func TestScanShellTextPositionAndLexing(t *testing.T) {
	runScanCases(t, []scanCase{
		{name: "echo is not command position", text: `echo docker compose -p "${A}-x"`},
		{
			name: "sudo wrapper",
			text: `sudo -E docker compose -p "${A}-x" up`,
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{
			name: "env wrapper with assignment",
			text: `env X=1 docker compose -p "${A}-x" up`,
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{
			name: "reserved word before the command",
			text: `if true; then docker compose -p "${A}-x" up; fi`,
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{
			name: "subshell",
			text: `(cd app && docker compose -p "${A}-x" up)`,
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{
			name: "redirection ampersand does not split",
			text: `docker compose -p "${A}-x" ps 2>&1 | grep web`,
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{name: "escaped semicolon does not split", text: `echo a\; docker compose -p "${A}-x" up`},
		{
			name: "quoted string spanning a continuation",
			text: "docker compose -p \"${A}-\\\nx\" up\n",
			want: []hit{{Line: 1, Value: "${A}-x"}},
		},
		{
			name: "two here-docs in one text",
			text: "cat <<A <<'B'\n" +
				"docker compose -p \"${A}-x\" up\n" +
				"A\n" +
				"docker compose -p \"${B}-x\" up\n" +
				"B\n" +
				"docker compose -p \"${C}-y\" up\n",
			want: []hit{{Line: 6, Value: "${C}-y"}},
		},
		{
			name: "tab-stripped here-doc",
			text: "cat <<-EOF\n\tdocker compose -p \"${A}-x\" up\n\tEOF\ndocker compose -p \"${C}-y\" up\n",
			want: []hit{{Line: 4, Value: "${C}-y"}},
		},
		{
			name: "same composite assigned twice",
			text: "PROJECT=\"${A}-x\"\nPROJECT=\"${B}-x\"\ndocker compose -p \"$PROJECT\" up\n",
			want: []hit{{Line: 3, Value: "${A}-x"}},
		},
		{
			name: "assignments disagree across control flow",
			text: "PROJECT=\"${A}-x\"; false && PROJECT=\"$COMPOSE_PROJECT_NAME\"\n" +
				"docker compose -p \"$PROJECT\" up\n",
		},
		{
			name: "hits on separate commands of one line",
			text: `docker compose -p "${A}-x" up; docker compose -p "${B}-y" ps`,
			want: []hit{{Line: 1, Value: "${A}-x"}, {Line: 1, Value: "${B}-y"}},
		},
	})
}
