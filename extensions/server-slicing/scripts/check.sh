#!/bin/sh
# The server-slicing module, built, installed and driven end to end.
#
#   extensions/server-slicing  a module with a Go half and a browser half, in
#                              its own Go module, that slices 3MF projects
#                              with PrusaSlicer
#
# What it does, in order:
#
#   1. builds, vets and tests the Go half inside its own folder, which is what
#      somebody who cloned the module alone does
#   2. holds the typed client to the module's OpenAPI fragment
#   3. lints and type-checks the browser half, runs its tests in Chromium,
#      and checks every component has its stories
#   4. lays the module out as a release and installs it with
#      `prusa-connect module add`, naming the stand-in slicer under
#      testdata as a local binary
#   5. starts the demo, signs in, checks that the approved install granted
#      the module write_storage on the storages, uploads a project into the
#      demo's storage, asks the module to slice its second bed, and reads the
#      G-code back from the storage
#
# The real PrusaSlicer is not run here: it needs a download of a few hundred
# megabytes and a Mac. `make integration` runs it.
#
# The whole run is one function, called on the last line, so the shell parses
# this file to its end before executing any of it.
main() {

ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
cd "$ROOT" || exit 1

MODULE_DIR=extensions/server-slicing
CLIENT_DIR=$MODULE_DIR/client
UI_DIR=$MODULE_DIR/ui

# The component library the bundle is built against, and the client SDK the
# typed client links to. A fresh clone has no dist under either.
LIBRARY_DIR=packages/svelte
CLIENT_SDK_DIR=packages/typescript

# Everything this run writes goes under bin/, which git ignores, and that is
# where a build artefact belongs. One run at a time owns it: a second run that
# starts while the first is going stops at the lock below.
WORK_ROOT=bin/server-slicing
LOCK=$WORK_ROOT.lock
RELEASE=$WORK_ROOT/release
MODULES_DIR=$WORK_ROOT/modules
DEMO_DIR=$WORK_ROOT/demo
OUT_CONFIG=${SLICER_CONFIG:-$WORK_ROOT/config.yaml}

# The module path the module is installed from, and the namespace the host
# derives from it. The author never writes the namespace: it is the reversed
# module path, and every address of the module is built from it.
SLICER_MODULE=github.com/prusa3d/server-slicing
SLICER_NAMESPACE=com.github.aux.prusa3d.server-slicing
MOUNT=/api/v1/modules/$SLICER_NAMESPACE

# The demo binds the first free port from SLICER_PORT up, 18298 by default. It
# is not 8080, so a run does not collide with a server somebody already has up.
# DEMO_ADDR and BASE_URL are set once a port is chosen, before the demo starts.
FIRST_PORT=${SLICER_PORT:-18298}


CHECKSUMS_FILE=checksums.txt

# CI=true keeps pnpm from asking anything, and keeps its output readable.
CI=true
export CI

for tool in go pnpm sha256sum curl bash; do
	if ! command -v "$tool" > /dev/null 2>&1; then
		echo "FAIL  $tool is not on PATH, and this lane needs it."
		exit 1
	fi
done

WORK=$(mktemp -d)
DEMO_PID=

# cleanup runs on every way out: success, a failed step, Ctrl-C and a kill. It
# stops the demo this run started, removes the scratch files and gives up the
# lock. A signal exits with 128 plus its number, so a caller sees the run as
# interrupted and not as passed.
cleanup() {
	if [ -n "$DEMO_PID" ]; then
		kill "$DEMO_PID" 2> /dev/null
		wait "$DEMO_PID" 2> /dev/null
	fi
	rm -rf "$WORK"
	if [ "$(cat "$LOCK/pid" 2> /dev/null)" = "$$" ]; then
		rm -rf "$LOCK"
	fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# take_lock makes $LOCK, which only one process can do. A lock whose run has
# ended is stale: that run was killed before its cleanup. Its demo may still be
# up, so it is stopped here, before this run deletes the files under it.
take_lock() {
	mkdir -p "$(dirname "$LOCK")"
	if mkdir "$LOCK" 2> /dev/null; then
		echo "$$" > "$LOCK/pid"
		return 0
	fi

	owner=$(cat "$LOCK/pid" 2> /dev/null)
	if [ -n "$owner" ] && kill -0 "$owner" 2> /dev/null; then
		echo "FAIL  another run of this check (pid $owner) is using $WORK_ROOT."
		echo "      Wait for it to end, or stop it."
		exit 1
	fi

	stale=$(cat "$LOCK/demo.pid" 2> /dev/null)
	if [ -n "$stale" ] && ps -p "$stale" -o command= 2> /dev/null | grep -q demo-server; then
		echo "    stopping the demo (pid $stale) that an interrupted run left running"
		kill "$stale" 2> /dev/null
		sleep 1
	fi

	rm -rf "$LOCK"
	if ! mkdir "$LOCK" 2> /dev/null; then
		echo "FAIL  another run of this check took $LOCK first."
		exit 1
	fi
	echo "$$" > "$LOCK/pid"
}

take_lock

STEPS=0
step() {
	WHAT=$1
	shift
	STEPS=$((STEPS + 1))

	printf '%2d. %s...\n' "$STEPS" "$WHAT"

	if "$@" > "$WORK/step.log" 2>&1; then
		return 0
	fi

	echo "FAIL  $WHAT"
	cat "$WORK/step.log"
	exit 1
}

rm -rf "$WORK_ROOT"
mkdir -p "$MODULES_DIR" "$DEMO_DIR" "$RELEASE"

# The version the manifest declares. `module add` refuses a release whose
# manifest says another one, so reading it here is the only way the two agree
# without being kept in step by hand.
SLICER_VERSION=$(sed -n 's/^version:[ 	]*//p' "$MODULE_DIR/module.yaml" | head -1)
if [ -z "$SLICER_VERSION" ]; then
	echo "FAIL  $MODULE_DIR/module.yaml declares no version"
	exit 1
fi

echo " 0. the server-slicing module declares version $SLICER_VERSION"

# ── The Go half, inside its own folder ────────────────────────────────
# The module carries its own go.mod, so the core's `go build ./...` walks past
# it. These three are the build a person who cloned the module alone runs.
step "the Go half builds and vets in its own folder" sh -c \
	"cd '$MODULE_DIR' && go build ./... && go vet ./..."
step "the Go half's rules, routes, manifest and changelog hold" sh -c \
	"cd '$MODULE_DIR' && go test -count=1 ./..."

# ── The two packages the browser half links to ───────────────────────
if [ ! -f "$LIBRARY_DIR/dist/build/module.js" ]; then
	step "the component library builds" sh -c \
		"cd '$LIBRARY_DIR' && pnpm install --frozen-lockfile && pnpm run build"
fi

if [ ! -f "$CLIENT_SDK_DIR/dist/index.js" ]; then
	step "the client SDK builds" sh -c \
		"cd '$CLIENT_SDK_DIR' && pnpm install --frozen-lockfile && pnpm run build"
fi

# ── The typed client ─────────────────────────────────────────────────
# It is generated from the same fragment the module embeds, so -check fails
# when the fragment moved and nobody regenerated.
step "the typed client is what the fragment generates" \
	go run ./cmd/generate-client-sdk -module -check \
	-document "$MODULE_DIR/openapi.json" \
	-namespace "$SLICER_NAMESPACE" -name server-slicing-client -out "$CLIENT_DIR"

step "the typed client installs" sh -c "cd '$CLIENT_DIR' && pnpm install --frozen-lockfile"
step "the client types match the fragment" sh -c \
	"cd '$CLIENT_DIR' && pnpm run generate:types && git diff --exit-code src/generated/"
step "the typed client builds" sh -c "cd '$CLIENT_DIR' && pnpm run build"

# ── The browser half ─────────────────────────────────────────────────
step "the browser half installs" sh -c "cd '$UI_DIR' && pnpm install --frozen-lockfile"

# The browser tests run in the Chromium of the browser half's own playwright,
# so that is the copy installed. On a GitHub runner, which is Linux, the
# libraries Chromium needs are installed with it.
PLAYWRIGHT_FLAGS=
if [ "${GITHUB_ACTIONS:-}" = true ] && [ "$(uname -s)" = Linux ]; then
	PLAYWRIGHT_FLAGS=--with-deps
fi
step "Chromium for the browser tests installs" sh -c \
	"cd '$UI_DIR' && pnpm exec playwright install $PLAYWRIGHT_FLAGS chromium"
step "the browser half lints, type-checks and its tests pass in Chromium" sh -c \
	"cd '$UI_DIR' && pnpm run check"
step "the browser half builds into a bundle" sh -c "cd '$UI_DIR' && pnpm run build"

if [ ! -f "$UI_DIR/dist/module.yaml" ] || [ ! -f "$UI_DIR/dist/ui/index.js" ]; then
	echo "FAIL  $UI_DIR/dist is missing module.yaml or ui/index.js"
	exit 1
fi

# ── The release ──────────────────────────────────────────────────────
# The layout `module add` reads: the manifest at the root, this platform's
# binary beside it, the built bundle under ui/, and a checksums.txt over all of
# them. The file name of the binary is <name>_<GOOS>_<GOARCH>, which is what
# the host looks for in the directory the declaration points at.
BINARY="server-slicing_$(go env GOOS)_$(go env GOARCH)$(go env GOEXE)"

pack() {
	# The output path is relative to the module folder. An absolute one would
	# be this shell's own spelling of it, and under Git Bash on Windows that
	# is a path the Go toolchain cannot open.
	( cd "$MODULE_DIR" && go build -o "../../$WORK_ROOT/$BINARY" . ) || return 1
	sh extensions/scripts/pack-release.sh "$UI_DIR/dist" "$WORK_ROOT/$BINARY" "$RELEASE"
}

step "the release packs" pack

if [ ! -f "$RELEASE/module.yaml" ] || [ ! -f "$RELEASE/ui/index.js" ] || [ ! -f "$RELEASE/$CHECKSUMS_FILE" ]; then
	echo "FAIL  $RELEASE is missing module.yaml, ui/index.js or checksums.txt"
	exit 1
fi

# ── The operator's file ──────────────────────────────────────────────
# It names where the machine-owned overlay goes and where modules are
# installed. The module keeps its slicers and jobs in its own data directory,
# which the host makes beside the modules directory. The storage it slices
# into is the demo's own `local`.
#
# Every path here is relative to the directory the server is started from,
# which is the repository root.
cat > "$OUT_CONFIG" <<- YAML
	core:
	  overlay: $DEMO_DIR/resources.json
	  modules: $MODULES_DIR
YAML

CORE=$WORK_ROOT/prusa-connect$(go env GOEXE)
# The binary is not called demo: $DEMO_DIR is $WORK_ROOT/demo, and on Linux,
# where GOEXE is empty, a binary of that name and the directory would be one
# path.
DEMO=$WORK_ROOT/demo-server$(go env GOEXE)

# Both are stamped with the version `make build` stamps. The module's core
# range is held against it, and an unstamped build reads a version Go made up
# from the commit, which no range names.
STAMP="-X github.com/prusa3d/prusa-connect/internal/kernel/version.stamped=$(sh scripts/build-version.sh)"

step "the core and the demo build" sh -c \
	"go build -ldflags '$STAMP' -o '$CORE' ./cmd/prusa-connect && \
	 go build -ldflags '$STAMP' -o '$DEMO' ./cmd/demo"

# The stand-in slicer under testdata answers --help as PrusaSlicer 2.9.6 and
# writes a small binary G-code file, so the run needs no download. The path is
# absolute, as the setting takes one.
STAND_IN=$ROOT/$MODULE_DIR/testdata/fake-slicer.sh
printf '{"local_binaries":["%s"],"github_releases":false}\n' "$STAND_IN" > "$WORK/settings.json"

# -approve grants the data directory and the permissions module.yaml lists:
# the viewer role, which reaches read_storage, and write_storage on the
# storage kind (ADR-20261027-pq5).
step "the server-slicing module installs" "$CORE" module add "$SLICER_MODULE@$SLICER_VERSION" \
	-config "$OUT_CONFIG" -from "$RELEASE" -approve -settings "$WORK/settings.json"

step "the listing on disk holds one line for it" sh -c \
	"'$CORE' module list -config '$OUT_CONFIG' | tee '$WORK/list.txt' && \
	 grep -q '$SLICER_NAMESPACE' '$WORK/list.txt'"


# ── The demo ─────────────────────────────────────────────────────────
port_free() {
	curl -sS -o /dev/null --max-time 2 "http://127.0.0.1:$1/" > /dev/null 2>&1
	[ "$?" -eq 7 ]
}

pick_port() {
	port=$FIRST_PORT
	while [ "$port" -lt $((FIRST_PORT + 50)) ]; do
		if port_free "$port"; then
			DEMO_ADDR=127.0.0.1:$port
			BASE_URL=http://$DEMO_ADDR
			return 0
		fi
		port=$((port + 1))
	done

	echo "no free port from $FIRST_PORT to $((FIRST_PORT + 49)) on 127.0.0.1" >&2
	return 1
}

step "a free port for the demo" pick_port
echo "    the demo binds $DEMO_ADDR"

start_demo() {
	"$DEMO" -config "$OUT_CONFIG" -dir "$DEMO_DIR" -addr "$DEMO_ADDR" \
		-printers 1 -flap 0 -cameras=false >> "$WORK/demo.log" 2>&1 &
	DEMO_PID=$!
	echo "$DEMO_PID" > "$LOCK/demo.pid"
}

stop_demo() {
	[ -n "$DEMO_PID" ] || return 0
	kill "$DEMO_PID" 2> /dev/null
	wait "$DEMO_PID" 2> /dev/null
	DEMO_PID=
	rm -f "$LOCK/demo.pid"
}

ready() {
	waited=0
	while [ "$waited" -lt 120 ]; do
		if ! kill -0 "$DEMO_PID" 2> /dev/null; then
			echo "the demo ended before it served" >&2
			cat "$WORK/demo.log" >&2
			return 1
		fi

		if curl -fsS "$BASE_URL/api/v1/health" > /dev/null 2>&1; then
			return 0
		fi

		waited=$((waited + 1))
		sleep 1
	done

	cat "$WORK/demo.log" >&2
	return 1
}

read_password() {
	waited=0
	while [ "$waited" -lt 60 ]; do
		PASSWORD=$(sed -n 's/^ *password  *//p' "$WORK/demo.log" | head -1)
		if [ -n "$PASSWORD" ]; then
			return 0
		fi

		waited=$((waited + 1))
		sleep 1
	done

	cat "$WORK/demo.log" >&2
	return 1
}

sign_in() {
	waited=0
	while [ "$waited" -lt 60 ]; do
		rm -f "$WORK/cookies"

		if curl -fsS -c "$WORK/cookies" -X POST "$BASE_URL/api/v1/auth/login" \
			-H 'content-type: application/json' \
			-d "{\"username\":\"demo\",\"password\":\"$PASSWORD\"}" > /dev/null 2>&1; then
			return 0
		fi

		waited=$((waited + 1))
		sleep 1
	done

	echo "the demo account did not sign in with the password the demo printed" >&2
	return 1
}

# ready waits until the module answers its status and lists the stand-in.
# The process takes a moment to start and to ask the stand-in its version.
slicer_listed() {
	waited=0
	while [ "$waited" -lt 60 ]; do
		if curl -fsS -b "$WORK/cookies" "$BASE_URL$MOUNT/slicers" > "$WORK/slicers.json" 2> /dev/null &&
			grep -q '"default":"2.9.6"' "$WORK/slicers.json"; then
			return 0
		fi

		waited=$((waited + 1))
		sleep 1
	done

	cat "$WORK/slicers.json" >&2
	tail -n 40 "$WORK/demo.log" >&2
	return 1
}

# write_granted reads the grants on the storage kind and finds the one the
# approved install wrote for the module. Nothing in this run writes it.
write_granted() {
	curl -fsS -b "$WORK/cookies" "$BASE_URL/api/v1/grants/resource_kind/storage" > "$WORK/grants.json" &&
		grep -q '"sink:server-slicing"' "$WORK/grants.json" ||
		{ echo "the storage kind carries no grant for sink:server-slicing:" >&2; cat "$WORK/grants.json" >&2; return 1; }
}

# upload puts the project into the demo's storage as the signed-in person.
# The demo answers before its storage is ready, and until then the core
# answers a 503 it documents as retryable.
upload() {
	tries=0
	while :; do
		tries=$((tries + 1))
		status=$(curl -sS -o "$WORK/upload.json" -w '%{http_code}' -b "$WORK/cookies" \
			-X PUT "$BASE_URL/api/v1/storages/local/content?path=gate/three-cubes.3mf" \
			-H 'content-type: application/octet-stream' --data-binary @"$MODULE_DIR/testdata/three-cubes.3mf")
		[ "$status" = 200 ] || [ "$status" = 201 ] && return 0
		if [ "$status" != 503 ] || [ "$tries" -ge 10 ]; then
			echo "the upload answered $status after $tries attempt(s):" >&2
			cat "$WORK/upload.json" >&2
			return 1
		fi
		sleep 1
	done
}

# slice asks for the second bed of the project and waits for the job to end.
# The worker reads the project and writes the G-code as the module, through
# storage.write, so the G-code read back below crossed the real host.
slice() {
	curl -fsS -b "$WORK/cookies" -X POST "$BASE_URL$MOUNT/jobs" -H 'content-type: application/json' \
		-d '{"source":{"storage":"local","path":"gate/three-cubes.3mf"},"selection":{"bed":1}}' > "$WORK/job.json" || return 1

	JOB_ID=$(sed -n 's/.*"id":"\([0-9a-f]*\)".*/\1/p' "$WORK/job.json")
	[ -n "$JOB_ID" ] || { cat "$WORK/job.json" >&2; return 1; }

	waited=0
	while [ "$waited" -lt 60 ]; do
		curl -fsS -b "$WORK/cookies" "$BASE_URL$MOUNT/jobs/$JOB_ID" > "$WORK/job.json" || return 1

		if grep -q '"state":"done"' "$WORK/job.json"; then
			# The name starts with a hash of the target (README.md, What a job writes).
			OUTPUT=$(grep -o '"path":"gate/[0-9a-f]\{8\}-three-cubes\.bgcode"' "$WORK/job.json" | head -n 1 | cut -d'"' -f4)
			[ -n "$OUTPUT" ] && return 0
		fi

		if grep -q '"state":"failed"\|"state":"canceled"' "$WORK/job.json"; then
			break
		fi

		waited=$((waited + 1))
		sleep 1
	done

	cat "$WORK/job.json" >&2
	tail -n 40 "$WORK/demo.log" >&2
	return 1
}

# read_back fetches the G-code from the storage and checks it is binary.
read_back() {
	curl -fsS -b "$WORK/cookies" -o "$WORK/result.bgcode" \
		"$BASE_URL/api/v1/storages/local/content?path=$OUTPUT" &&
		[ "$(head -c 4 "$WORK/result.bgcode")" = GCDE ]
}

# not_offered asks to install a version no manifest offers. The demo user is
# an administrator, so the refusal is the version's and not the person's.
not_offered() {
	status=$(curl -sS -o "$WORK/install.json" -w '%{http_code}' -b "$WORK/cookies" \
		-X POST "$BASE_URL$MOUNT/slicers/install" -H 'content-type: application/json' -d '{"version":"9.9.9"}')
	[ "$status" = 404 ] && grep -q 'server-slicing/VERSION_NOT_OFFERED' "$WORK/install.json"
}

# scoped mints a token with one scope for the demo account.
scoped() {
	curl -fsS -b "$WORK/cookies" -X POST "$BASE_URL/api/v1/auth/credentials" -H 'content-type: application/json' \
		-d "{\"name\":\"gate $1\",\"scopes\":[\"$1\"]}" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'
}

# scopes holds the routes to the scope of the credential: a read token lists
# the jobs and slices nothing, and an operate token slices and installs
# nothing. The demo account is an administrator, so each refusal is the
# scope's.
scopes() {
	READ_TOKEN=$(scoped read)
	OPERATE_TOKEN=$(scoped operate)
	[ -n "$READ_TOKEN" ] && [ -n "$OPERATE_TOKEN" ] || return 1

	status=$(curl -sS -o /dev/null -w '%{http_code}' -H "authorization: Bearer $READ_TOKEN" "$BASE_URL$MOUNT/jobs")
	[ "$status" = 200 ] || { echo "a read token listing the jobs answered $status" >&2; return 1; }

	status=$(curl -sS -o "$WORK/scoped.json" -w '%{http_code}' -H "authorization: Bearer $READ_TOKEN" \
		-X POST "$BASE_URL$MOUNT/jobs" -H 'content-type: application/json' \
		-d '{"source":{"storage":"local","path":"gate/three-cubes.3mf"}}')
	[ "$status" = 403 ] || { echo "a read token slicing answered $status" >&2; cat "$WORK/scoped.json" >&2; return 1; }

	status=$(curl -sS -o "$WORK/scoped.json" -w '%{http_code}' -H "authorization: Bearer $OPERATE_TOKEN" \
		-X POST "$BASE_URL$MOUNT/slicers/install" -H 'content-type: application/json' -d '{"version":"2.9.6"}')
	[ "$status" = 403 ] && grep -q 'server-slicing/NOT_ADMINISTRATOR' "$WORK/scoped.json" ||
		{ echo "an operate token installing answered $status" >&2; cat "$WORK/scoped.json" >&2; return 1; }
}

start_demo
step "the demo serves" ready
step "the demo prints an account to sign in with" read_password
step "the demo account signs in" sign_in
step "the module lists the stand-in slicer as the default" slicer_listed

step "the listing publishes the bundle under the same namespace" sh -c \
	"curl -fsS -b '$WORK/cookies' '$BASE_URL/api/v1/resources' > '$WORK/resources.json' && \
	 grep -q '\"entry_url\":\"$MOUNT/ui/ui/index.js\"' '$WORK/resources.json'"

step "the bundle answers" sh -c \
	"curl -fsS -o /dev/null -b '$WORK/cookies' '$BASE_URL$MOUNT/ui/ui/index.js'"

step "the approved install granted the module write_storage" write_granted
step "a project is uploaded into the demo's storage" upload
step "the module slices the project's second bed into the storage" slice
step "the G-code is read back from the storage" read_back
step "a version no manifest offers is refused" not_offered
step "a read token reads, and an operate token installs nothing" scopes

stop_demo

echo "OK    the server-slicing module passed $STEPS steps"
echo
echo "      the release     $RELEASE"
echo "      installed into  $MODULES_DIR"
echo "      the config      $OUT_CONFIG"

}
main "$@"
