package docker

import (
	"reflect"
	"testing"
)

func TestExternalBaseRefs(t *testing.T) {
	tests := []struct {
		name       string
		dockerfile string
		buildArgs  map[string]string
		want       []string
	}{
		{
			name:       "simple FROM",
			dockerfile: "FROM golang:1.22\nRUN go build ./...\n",
			want:       []string{"golang:1.22"},
		},
		{
			name: "multi-stage with AS and stage reuse",
			dockerfile: `FROM golang:1.22 AS build
RUN go build -o /out ./...
FROM build AS test
RUN go test ./...
FROM alpine:3.19
COPY --from=build /out /out
`,
			want: []string{"alpine:3.19", "golang:1.22"},
		},
		{
			name:       "scratch excluded",
			dockerfile: "FROM golang:1.22 AS build\nFROM scratch\nCOPY --from=build /out /out\n",
			want:       []string{"golang:1.22"},
		},
		{
			name:       "scratch excluded case-insensitive",
			dockerfile: "FROM SCRATCH\n",
			want:       nil,
		},
		{
			name:       "ARG default used in FROM",
			dockerfile: "ARG BASE_IMAGE=golang:1.22\nFROM ${BASE_IMAGE}\n",
			want:       []string{"golang:1.22"},
		},
		{
			name:       "buildArgs override beats ARG default",
			dockerfile: "ARG BASE_IMAGE=golang:1.22\nFROM ${BASE_IMAGE}\n",
			buildArgs:  map[string]string{"BASE_IMAGE": "golang:1.23"},
			want:       []string{"golang:1.23"},
		},
		{
			name:       "${VAR:-def} fallback when not declared",
			dockerfile: "FROM ${BASE_IMAGE:-golang:1.22}\n",
			want:       []string{"golang:1.22"},
		},
		{
			name:       "${VAR:-def} fallback overridden by buildArgs when declared",
			dockerfile: "ARG BASE_IMAGE\nFROM ${BASE_IMAGE:-golang:1.22}\n",
			buildArgs:  map[string]string{"BASE_IMAGE": "golang:1.23"},
			want:       []string{"golang:1.23"},
		},
		{
			name:       "--platform flag on FROM",
			dockerfile: "FROM --platform=linux/amd64 golang:1.22 AS build\n",
			want:       []string{"golang:1.22"},
		},
		{
			name: "line continuation inside FROM/ARG",
			dockerfile: "ARG BASE_IMAGE=golang:1.\\\n22\n" +
				"FROM ${BASE_IMAGE} \\\n    AS build\n",
			want: []string{"golang:1.22"},
		},
		{
			name: "comments interleaved",
			dockerfile: `# this is a Dockerfile
FROM golang:1.22 AS build
# a comment between stages
FROM alpine:3.19
# trailing comment
`,
			want: []string{"alpine:3.19", "golang:1.22"},
		},
		{
			name:       "$VAR bare form substitution",
			dockerfile: "ARG BASE_IMAGE=golang:1.22\nFROM $BASE_IMAGE\n",
			want:       []string{"golang:1.22"},
		},
		{
			name:       "dedupe repeated refs",
			dockerfile: "FROM golang:1.22 AS a\nFROM golang:1.22 AS b\n",
			want:       []string{"golang:1.22"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := externalBaseRefs([]byte(tt.dockerfile), tt.buildArgs)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("externalBaseRefs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestExternalBaseRefs_EdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		dockerfile string
		buildArgs  map[string]string
		want       []string
	}{
		{
			name:       "unresolvable ${UNSET} ref skipped",
			dockerfile: "FROM ${UNSET}\n",
			want:       nil,
		},
		{
			name:       "unresolvable ${UNSET} ref skipped but later FROM still collected",
			dockerfile: "FROM ${UNSET}\nFROM golang:1.22\n",
			want:       []string{"golang:1.22"},
		},
		{
			name:       "$BUILDPLATFORM builtin skipped (not declared via ARG)",
			dockerfile: "FROM --platform=$BUILDPLATFORM golang:1.22 AS build\n",
			want:       []string{"golang:1.22"},
		},
		{
			name:       "empty file has no refs",
			dockerfile: "",
			want:       nil,
		},
		{
			name:       "escape directive honored",
			dockerfile: "# escape=`\nARG BASE_IMAGE=golang:1.`\n22\nFROM ${BASE_IMAGE}\n",
			want:       []string{"golang:1.22"},
		},
		{
			name:       "lowercase from/arg recognized",
			dockerfile: "arg base_image=golang:1.22\nfrom ${base_image}\n",
			want:       []string{"golang:1.22"},
		},
		{
			name:       "ARG after first FROM is stage-scoped, not collected as global default",
			dockerfile: "FROM golang:1.22 AS build\nARG BASE_IMAGE=alpine:3.19\nFROM ${BASE_IMAGE}\n",
			want:       []string{"golang:1.22"},
		},
		{
			name:       "stage reference matched case-insensitively",
			dockerfile: "FROM golang:1.22 AS Build\nFROM build\nRUN true\n",
			want:       []string{"golang:1.22"},
		},
		{
			name:       "unsupported ${VAR:+alt} expansion form skipped",
			dockerfile: "ARG FLAG\nFROM ${FLAG:+alpine:3.19}\nFROM golang:1.22\n",
			want:       []string{"golang:1.22"},
		},
		{
			name:       "escape directive after syntax directive honored",
			dockerfile: "# syntax=docker/dockerfile:1\n# escape=`\nARG BASE_IMAGE=golang:1.`\n22\nFROM ${BASE_IMAGE}\n",
			want:       []string{"golang:1.22"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := externalBaseRefs([]byte(tt.dockerfile), tt.buildArgs)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("externalBaseRefs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
