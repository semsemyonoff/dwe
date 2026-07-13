package docker

import (
	"reflect"
	"testing"
)

// bases builds a []BaseRef with empty platforms from bare ref names.
func bases(names ...string) []BaseRef {
	out := make([]BaseRef, len(names))
	for i, n := range names {
		out[i] = BaseRef{Ref: n}
	}
	return out
}

func TestExternalBaseRefs(t *testing.T) {
	tests := []struct {
		name           string
		dockerfile     string
		buildArgs      map[string]string
		targetPlatform string
		want           []BaseRef
	}{
		{
			name:       "simple FROM",
			dockerfile: "FROM golang:1.22\nRUN go build ./...\n",
			want:       bases("golang:1.22"),
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
			want: bases("alpine:3.19", "golang:1.22"),
		},
		{
			name:       "scratch excluded",
			dockerfile: "FROM golang:1.22 AS build\nFROM scratch\nCOPY --from=build /out /out\n",
			want:       bases("golang:1.22"),
		},
		{
			name:       "scratch excluded case-insensitive",
			dockerfile: "FROM SCRATCH\n",
			want:       nil,
		},
		{
			name:       "ARG default used in FROM",
			dockerfile: "ARG BASE_IMAGE=golang:1.22\nFROM ${BASE_IMAGE}\n",
			want:       bases("golang:1.22"),
		},
		{
			name:       "buildArgs override beats ARG default",
			dockerfile: "ARG BASE_IMAGE=golang:1.22\nFROM ${BASE_IMAGE}\n",
			buildArgs:  map[string]string{"BASE_IMAGE": "golang:1.23"},
			want:       bases("golang:1.23"),
		},
		{
			name:       "${VAR:-def} fallback when not declared",
			dockerfile: "FROM ${BASE_IMAGE:-golang:1.22}\n",
			want:       bases("golang:1.22"),
		},
		{
			name:       "${VAR:-def} fallback overridden by buildArgs when declared",
			dockerfile: "ARG BASE_IMAGE\nFROM ${BASE_IMAGE:-golang:1.22}\n",
			buildArgs:  map[string]string{"BASE_IMAGE": "golang:1.23"},
			want:       bases("golang:1.23"),
		},
		{
			name:       "${VAR:-def} fallback when buildArgs override is empty",
			dockerfile: "ARG BASE_IMAGE\nFROM ${BASE_IMAGE:-golang:1.22}\n",
			buildArgs:  map[string]string{"BASE_IMAGE": ""},
			want:       bases("golang:1.22"),
		},
		{
			name:       "${VAR:-def} fallback when ARG default is empty",
			dockerfile: "ARG BASE_IMAGE=\nFROM ${BASE_IMAGE:-golang:1.22}\n",
			want:       bases("golang:1.22"),
		},
		{
			name:       "--platform flag on FROM is captured",
			dockerfile: "FROM --platform=linux/amd64 golang:1.22 AS build\n",
			want:       []BaseRef{{Ref: "golang:1.22", Platform: "linux/amd64"}},
		},
		{
			name:       "--platform flag resolved from ARG",
			dockerfile: "ARG TP=linux/arm64\nFROM --platform=${TP} golang:1.22 AS build\n",
			want:       []BaseRef{{Ref: "golang:1.22", Platform: "linux/arm64"}},
		},
		{
			name:       "same ref different platforms kept distinct",
			dockerfile: "FROM --platform=linux/amd64 golang:1.22 AS a\nFROM --platform=linux/arm64 golang:1.22 AS b\n",
			want: []BaseRef{
				{Ref: "golang:1.22", Platform: "linux/amd64"},
				{Ref: "golang:1.22", Platform: "linux/arm64"},
			},
		},
		{
			name:           "service target platform applies to bare FROM",
			dockerfile:     "FROM golang:1.22\nRUN go build ./...\n",
			targetPlatform: "linux/amd64",
			want:           []BaseRef{{Ref: "golang:1.22", Platform: "linux/amd64"}},
		},
		{
			name:           "explicit FROM --platform overrides service target platform",
			dockerfile:     "FROM --platform=linux/arm64 golang:1.22 AS build\n",
			targetPlatform: "linux/amd64",
			want:           []BaseRef{{Ref: "golang:1.22", Platform: "linux/arm64"}},
		},
		{
			name:           "$TARGETPLATFORM resolves to service target platform",
			dockerfile:     "FROM --platform=$TARGETPLATFORM golang:1.22 AS build\n",
			targetPlatform: "linux/amd64",
			want:           []BaseRef{{Ref: "golang:1.22", Platform: "linux/amd64"}},
		},
		{
			name:           "${TARGETPLATFORM} braced form resolves to service target platform",
			dockerfile:     "FROM --platform=${TARGETPLATFORM} golang:1.22 AS build\n",
			targetPlatform: "linux/amd64",
			want:           []BaseRef{{Ref: "golang:1.22", Platform: "linux/amd64"}},
		},
		{
			name:           "$BUILDPLATFORM stays native even with service target platform",
			dockerfile:     "FROM --platform=$BUILDPLATFORM golang:1.22 AS build\n",
			targetPlatform: "linux/amd64",
			want:           bases("golang:1.22"),
		},
		{
			name: "line continuation inside FROM/ARG",
			dockerfile: "ARG BASE_IMAGE=golang:1.\\\n22\n" +
				"FROM ${BASE_IMAGE} \\\n    AS build\n",
			want: bases("golang:1.22"),
		},
		{
			name: "comments interleaved",
			dockerfile: `# this is a Dockerfile
FROM golang:1.22 AS build
# a comment between stages
FROM alpine:3.19
# trailing comment
`,
			want: bases("alpine:3.19", "golang:1.22"),
		},
		{
			name:       "$VAR bare form substitution",
			dockerfile: "ARG BASE_IMAGE=golang:1.22\nFROM $BASE_IMAGE\n",
			want:       bases("golang:1.22"),
		},
		{
			name:       "dedupe repeated refs",
			dockerfile: "FROM golang:1.22 AS a\nFROM golang:1.22 AS b\n",
			want:       bases("golang:1.22"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := externalBaseRefs([]byte(tt.dockerfile), tt.buildArgs, tt.targetPlatform)
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
		want       []BaseRef
	}{
		{
			name:       "unresolvable ${UNSET} ref skipped",
			dockerfile: "FROM ${UNSET}\n",
			want:       nil,
		},
		{
			name:       "unresolvable ${UNSET} ref skipped but later FROM still collected",
			dockerfile: "FROM ${UNSET}\nFROM golang:1.22\n",
			want:       bases("golang:1.22"),
		},
		{
			name:       "$BUILDPLATFORM builtin platform dropped, ref still collected",
			dockerfile: "FROM --platform=$BUILDPLATFORM golang:1.22 AS build\n",
			want:       bases("golang:1.22"),
		},
		{
			name:       "empty file has no refs",
			dockerfile: "",
			want:       nil,
		},
		{
			name:       "escape directive honored",
			dockerfile: "# escape=`\nARG BASE_IMAGE=golang:1.`\n22\nFROM ${BASE_IMAGE}\n",
			want:       bases("golang:1.22"),
		},
		{
			name:       "lowercase from/arg recognized",
			dockerfile: "arg base_image=golang:1.22\nfrom ${base_image}\n",
			want:       bases("golang:1.22"),
		},
		{
			name:       "ARG after first FROM is stage-scoped, not collected as global default",
			dockerfile: "FROM golang:1.22 AS build\nARG BASE_IMAGE=alpine:3.19\nFROM ${BASE_IMAGE}\n",
			want:       bases("golang:1.22"),
		},
		{
			name:       "stage reference matched case-insensitively",
			dockerfile: "FROM golang:1.22 AS Build\nFROM build\nRUN true\n",
			want:       bases("golang:1.22"),
		},
		{
			name:       "unsupported ${VAR:+alt} expansion form skipped",
			dockerfile: "ARG FLAG\nFROM ${FLAG:+alpine:3.19}\nFROM golang:1.22\n",
			want:       bases("golang:1.22"),
		},
		{
			name:       "escape directive after syntax directive honored",
			dockerfile: "# syntax=docker/dockerfile:1\n# escape=`\nARG BASE_IMAGE=golang:1.`\n22\nFROM ${BASE_IMAGE}\n",
			want:       bases("golang:1.22"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := externalBaseRefs([]byte(tt.dockerfile), tt.buildArgs, "")
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("externalBaseRefs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
