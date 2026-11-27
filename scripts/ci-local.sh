#!/bin/sh
# Runs every CI job that can run on this machine, cheapest first, and lists
# every lane that did not run with the reason.
#
# Each lane calls the same `make` target the workflow step calls. Add a lane
# here when a step is added to .github/workflows/ci.yml.
#
# Usage: bash scripts/ci-local.sh --help
# RANGE, from the environment, is the commit range the changelog and commit
# message lanes judge. Empty means `@{upstream}..HEAD`.
#
# The whole run is one function, called on the last line, so the shell parses
# this file to its end before executing any of it. sh reads a script while it
# runs, and an edit during a run would otherwise change what runs.
main() {

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT" || exit 1

# Quoted wherever it is called: on Windows the path make exports holds a space.
MAKE=${MAKE:-make}

RANGE=${RANGE:-}

MODE=all
KEEP_GOING=0
LIST_ONLY=0
ONLY=""
CHANGED=0

# A third of the cores: most lanes are a `go test` or a bundler that already
# uses every core, so more lanes at once only queue for the same processors.
CORES=$(getconf _NPROCESSORS_ONLN 2>/dev/null || nproc 2>/dev/null || echo "${NUMBER_OF_PROCESSORS:-1}")
JOBS=$(( CORES / 3 ))
[ "$JOBS" -ge 1 ] || JOBS=1

while [ $# -gt 0 ]; do
	arg=$1
	shift
	case $arg in
	--fast) MODE=fast ;;
	--keep-going) KEEP_GOING=1 ;;
	--list) LIST_ONLY=1 ;;
	--changed) CHANGED=1 ;;
	--only=*) ONLY=",$(echo "${arg#--only=}" | tr ' ' ',')," ;;
	--jobs=*) JOBS=${arg#--jobs=} ;;
	-j) JOBS=${1:-}; [ $# -gt 0 ] && shift ;;
	-j*) JOBS=${arg#-j} ;;
	--help|-h)
		echo "usage: bash scripts/ci-local.sh [--fast] [--keep-going] [--list] [--only=id,id] [--changed] [-j N]"
		echo "  --fast        only the lanes marked fast: generated files, the commit gates, lint, unit tests"
		echo "  --keep-going  run every lane instead of stopping at the first failure"
		echo "  --list        print the plan and the estimate, run nothing"
		echo "  --only=a,b    run these lanes by id, for a re-check after one repair"
		echo "  --changed     only the lanes whose CI job a pull request of this branch would start"
		echo "  -j N          run up to N lanes at once, each into bin/ci-local/<id>.log. --jobs=N is the same."
		echo "                The default is a third of the cores, $JOBS here. -j 1 runs one lane at a time"
		echo "                and prints its output here"
		echo "  RANGE=a..b    in the environment: the commits the changelog and commit-style lanes judge"
		exit 0
		;;
	*)
		echo "FAIL  unknown argument: $arg"
		echo "      bash scripts/ci-local.sh --help"
		exit 2
		;;
	esac
done

case $JOBS in
'' | *[!0-9]* | 0)
	echo "FAIL  -j takes a whole number of lanes, 1 or more, and got '$JOBS'."
	exit 2
	;;
esac

TMP=$(mktemp -d)

# macOS gives each user a TMPDIR about fifty characters long, and a Unix socket
# a test opens under it passes the 104-byte limit of a socket path.
LANE_TMP=$(mktemp -d /tmp/ci-local.XXXXXX)
TMPDIR=$LANE_TMP
export TMPDIR

trap 'rm -rf "$TMP" "$LANE_TMP"' EXIT
trap 'stop_lanes; exit 130' INT
trap 'stop_lanes; exit 143' TERM

# --changed sorts what this branch changes against origin/main into the areas
# of scripts/ci-areas.sh, the script CI's first job runs, so a lane runs when
# its CI job would. The change is the commits, the uncommitted edits and the
# files git does not track yet.
AREAS=""
if [ "$CHANGED" = 1 ]; then
	BASE=$(git merge-base origin/main HEAD) || {
		echo "FAIL  --changed needs origin/main. 'git fetch origin main' fetches it."
		exit 2
	}
	AREAS=$( { git diff --name-status -M "$BASE"
		git ls-files --others --exclude-standard | sed 's/^/A	/'; } |
		bash scripts/ci-areas.sh 2> /dev/null | awk -F= '$2 == "true" { printf " %s", $1 }')
	echo "ci-local: this branch touches the areas:${AREAS:- none}"
	AREAS="$AREAS "
fi

# One record per lane: state, id, seconds, CI job, note. Tab separated, because
# the note is a sentence.
: > "$TMP/results"
FAILED=0
STOPPED=0

record() {
	printf '%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" "$5" >> "$TMP/results"
}

# ── What this machine has ───────────────────────────────────────────────
#
# Probed once, before the plan is printed, so the plan and the run agree.
have() {
	command -v "$1" > /dev/null 2>&1
}

HAS_DOCKER=no
have docker && docker info > /dev/null 2>&1 && HAS_DOCKER=yes

# The Postgres fixture publishes PGFIXTURE_PORT, 55432 unless it is set. When
# another program here holds that port, the lanes that start the fixture move
# to the next free one. A fixture this stack already runs keeps its port.
port_answers() {
	bash -c "exec 3<>/dev/tcp/127.0.0.1/$1" 2> /dev/null ||
		bash -c "exec 3<>/dev/tcp/::1/$1" 2> /dev/null
}

if [ -z "${PGFIXTURE_PORT:-}" ] && [ "$HAS_DOCKER" = yes ]; then
	PGFIXTURE_PORT=$(docker compose -f docker-compose.local.yml port postgres-fixture 5432 2> /dev/null | sed -n '1s/.*://p')
	if [ -z "$PGFIXTURE_PORT" ]; then
		for port in $(seq 55432 55531); do
			port_answers "$port" && continue
			PGFIXTURE_PORT=$port
			break
		done
		if [ -z "$PGFIXTURE_PORT" ]; then
			echo "WARN  ports 55432 to 55531 are all taken here, so the lanes that start the Postgres fixture will fail."
		elif [ "$PGFIXTURE_PORT" != 55432 ]; then
			echo "ci-local: port 55432 is taken here, so the Postgres fixture publishes $PGFIXTURE_PORT."
		fi
	fi
	[ -n "$PGFIXTURE_PORT" ] && export PGFIXTURE_PORT
fi

HAS_CC=no
have "$(go env CC 2>/dev/null)" && HAS_CC=yes

HAS_NODE=no
have node && HAS_NODE=yes

HAS_PNPM=no
have pnpm && HAS_PNPM=yes

HAS_CURL=no
have curl && HAS_CURL=yes

HAS_FRONTEND_DEPS=no
[ -f frontend/node_modules/.modules.yaml ] && HAS_FRONTEND_DEPS=yes

HAS_DOCS_DEPS=no
[ -f docs/node_modules/astro/bin/astro.mjs ] && HAS_DOCS_DEPS=yes

HAS_GOVULNCHECK=no
have govulncheck && HAS_GOVULNCHECK=yes


# Playwright keeps its browsers in one of these folders, by platform.
HAS_CHROMIUM=no
for dir in "${PLAYWRIGHT_BROWSERS_PATH:-}" "${LOCALAPPDATA:-}/ms-playwright" \
	"$HOME/.cache/ms-playwright" "$HOME/Library/Caches/ms-playwright"; do
	[ -n "$dir" ] || continue
	for found in "$dir"/chromium-*; do
		[ -d "$found" ] && HAS_CHROMIUM=yes
	done
done

# The tests that open the real mDNS socket run when PCL_NETWORK_TESTS is 1,
# and CI sets it on its Linux runners. It is set here on Linux alone. On
# Windows every new test binary that joins a multicast group makes the
# firewall ask the person at the keyboard to allow it, so those tests skip
# there and say how to run them.
if [ "$(uname -s)" = Linux ]; then
	PCL_NETWORK_TESTS=1
	export PCL_NETWORK_TESTS
fi

# unmet returns 0 when a named requirement is absent, and sets WHY to the
# sentence the summary prints. Every requirement needs a sentence here.
WHY=""
unmet() {
	WHY=""
	case $1 in
	none) return 1 ;;
	docker)
		[ "$HAS_DOCKER" = yes ] && return 1
		WHY="no Docker daemon answers here, and this job does its work in a container."
		return 0
		;;
	race-engine)
		[ "$HAS_CC" = yes ] && return 1
		[ "$HAS_DOCKER" = yes ] && return 1
		WHY="the race detector needs cgo, $(go env CC) is not on PATH, and no Docker daemon answers either."
		return 0
		;;
	node)
		[ "$HAS_NODE" = yes ] && return 1
		WHY="node is not on PATH. Install the version in .nvmrc."
		return 0
		;;
	pnpm)
		[ "$HAS_PNPM" = yes ] && return 1
		WHY="pnpm is not on PATH, and the job installs its dependencies with it."
		return 0
		;;
	curl)
		[ "$HAS_CURL" = yes ] && return 1
		WHY="curl is not on PATH, and the job calls the running server with it."
		return 0
		;;
	chromium)
		[ "$HAS_CHROMIUM" = yes ] && return 1
		WHY="no Playwright Chromium is installed. 'pnpm exec playwright install chromium' in packages/svelte fetches it."
		return 0
		;;
	frontend-deps)
		[ "$HAS_FRONTEND_DEPS" = yes ] && return 1
		WHY="frontend/node_modules is not installed. 'make install-deps' installs it."
		return 0
		;;
	docs-deps)
		[ "$HAS_DOCS_DEPS" = yes ] && return 1
		WHY="the documentation dependencies are not installed. 'make install-docs-deps' installs it."
		return 0
		;;
	govulncheck)
		[ "$HAS_GOVULNCHECK" = yes ] && return 1
		WHY="govulncheck is not on PATH. 'go install golang.org/x/vuln/cmd/govulncheck@latest' installs it."
		return 0
		;;
	*)
		echo "FAIL  lane requirement '$1' has no probe in $0."
		exit 2
		;;
	esac
}

# job_touched says whether a CI job runs for the areas in AREAS. The areas of
# each job are the `needs.changes.outputs` its `if:` names in
# .github/workflows/ci.yml, the table of ADR-20260923-xcq rule 1. A job that
# names none runs on every change.
job_touched() {
	case $1 in
	"Repository /"*) return 0 ;;
	"Web / Sign-in at"*) need="sign_in" ;;
	"Documentation (Starlight)") need="go docs changes removed" ;;
	"Backend / Verify generated code") need="go docs" ;;
	"Web / Component library"* | "Web / Application") need="web" ;;
	"Web /"* | "Docker /"* | "Backend / Client packages"*) need="go web" ;;
	"Backend /"*) need="go" ;;
	*) return 0 ;;
	esac
	for area in $need; do
		case $AREAS in *" $area "*) return 0 ;; esac
	done
	return 1
}

# ── The lanes ───────────────────────────────────────────────────────────
#
# lane <id> <tier> <requirements> <estimate in seconds> <shares> <CI job> <command>
#
# The estimate is printed in the plan and is never a timeout. It is what the
# lane takes on a developer machine with a warm build cache, to the nearest
# five seconds. Keep each tier ordered by estimate, cheapest first, so a run
# that fails does so early.
#
# <shares> says which lanes may run beside this one under -j:
#   -        any lane
#   solo     no other lane. It writes a file every other lane may read: a
#            committed file it regenerates, or the web app the full build
#            embeds. Or it walks the whole tree, which other lanes add folders
#            to and delete them from. Or a test in it fails under load. Solo
#            lanes run after the others
#   compose  no other compose lane. These start and remove services of
#            docker-compose.local.yml, which is one compose project
#   js       no other js lane. These install into and build packages/svelte,
#            packages/typescript and frontend, and write the same node_modules,
#            dist folders and generated catalogues
#   printermodel  no other printermodel lane. These write the printer profile
#            under internal/printer/profile/printermodel and compile it in
# A lane may name more than one, separated by a space.
lane() {
	LANE_ID=$1
	LANE_TIER=$2
	LANE_NEEDS=$3
	LANE_COST=$4
	LANE_SHARES=$5
	LANE_JOB=$6
	LANE_CMD=$7

	for share in $LANE_SHARES; do
		case $share in
		- | solo | compose | js | printermodel) ;;
		*)
			echo "FAIL  lane $LANE_ID shares '$share', which the table in $0 does not describe."
			exit 2
			;;
		esac
	done

	if [ -n "$ONLY" ]; then
		case $ONLY in
		*",$LANE_ID,"*) ;;
		*)
			record filtered "$LANE_ID" 0 "$LANE_JOB" ""
			return 0
			;;
		esac
	fi

	if [ -n "$AREAS" ] && ! job_touched "$LANE_JOB"; then
		record untouched "$LANE_ID" 0 "$LANE_JOB" ""
		return 0
	fi

	if [ "$MODE" = fast ] && [ "$LANE_TIER" != fast ]; then
		record deferred "$LANE_ID" 0 "$LANE_JOB" ""
		return 0
	fi

	for need in $LANE_NEEDS; do
		if unmet "$need"; then
			record skipped "$LANE_ID" 0 "$LANE_JOB" "$WHY"
			return 0
		fi
	done

	if [ "$STOPPED" = 1 ]; then
		record unreached "$LANE_ID" 0 "$LANE_JOB" "$UNREACHED"
		return 0
	fi

	if [ "$LIST_ONLY" = 1 ]; then
		printf 'planned\t%s\t%s\t%s\t%s\t%s\n' "$LANE_ID" "$LANE_COST" "$LANE_JOB" "$LANE_TIER" "$LANE_SHARES" >> "$TMP/results"
		return 0
	fi

	if [ "$JOBS" -gt 1 ]; then
		record queued "$LANE_ID" 0 "$LANE_JOB" ""
		printf '%s\n' "$LANE_CMD" > "$QUEUE/$LANE_ID.cmd"
		printf '%s\n' "$LANE_SHARES" > "$QUEUE/$LANE_ID.shares"
		printf '%s\t%s\n' "$LANE_COST" "$LANE_JOB" > "$QUEUE/$LANE_ID.job"
		PENDING="$PENDING $LANE_ID"
		return 0
	fi

	echo ""
	echo "===================================================================="
	echo "ci-local: $LANE_ID  ($LANE_JOB, about ${LANE_COST}s)"
	echo "===================================================================="

	START=$(date +%s)
	RC=0
	# A subshell, so a lane that changes directory does not move the next lane.
	( eval "$LANE_CMD" ) || RC=$?
	ELAPSED=$(( $(date +%s) - START ))

	if [ "$RC" -eq 0 ]; then
		echo "OK    ci-local: $LANE_ID passed in ${ELAPSED}s"
		record passed "$LANE_ID" "$ELAPSED" "$LANE_JOB" ""
	else
		echo "FAIL  ci-local: $LANE_ID failed in ${ELAPSED}s, exit $RC. The CI job is: $LANE_JOB"
		record failed "$LANE_ID" "$ELAPSED" "$LANE_JOB" "exit $RC"
		FAILED=1
		[ "$KEEP_GOING" = 0 ] && STOPPED=1
	fi

	return 0
}

UNREACHED="the run stopped at the first failing lane. '--keep-going' runs the rest."

# ── Running lanes side by side ──────────────────────────────────────────
#
# Under -j, plan_lanes only queues each lane that would run, in table order.
# run_queue then starts a queued lane when fewer than JOBS run and no running
# lane shares a name with it, polling once a second. Each lane writes its exit
# code and seconds to $QUEUE/<id>.rc when it ends, and its output to LOGS.
#
# Job control puts each lane in a process group of its own, so stop_lanes can
# end a lane with every process under it. A background job of a script without
# it ignores Ctrl-C and outlives the run.
LOGS=bin/ci-local
QUEUE="$TMP/queue"
PENDING=""
RUNNING=""
HELD=" "

words() {
	echo $#
}

is_solo() {
	case " $1 " in *" solo "*) return 0 ;; esac
	return 1
}

can_start() {
	[ "$(words $RUNNING)" -lt "$JOBS" ] || return 1
	case $HELD in *" solo "*) return 1 ;; esac
	if is_solo "$1"; then
		[ "$(words $RUNNING)" -eq 0 ] && [ "$2" = no ]
		return
	fi
	for share in $1; do
		[ "$share" = - ] && continue
		case $HELD in *" $share "*) return 1 ;; esac
	done
	return 0
}

start_lane() {
	shares=$(cat "$QUEUE/$1.shares")
	IFS='	' read -r cost job < "$QUEUE/$1.job"
	for share in $shares; do
		[ "$share" = - ] || HELD="$HELD$share "
	done
	RUNNING="$RUNNING $1"
	echo "START ci-local: $1  ($job, about ${cost}s)"
	cmd=$(cat "$QUEUE/$1.cmd")
	{
		begun=$(date +%s)
		( eval "$cmd" ) > "$LOGS/$1.log" 2>&1 < /dev/null
		rc=$?
		echo "$rc $(( $(date +%s) - begun ))" > "$QUEUE/$1.rc.part"
		mv "$QUEUE/$1.rc.part" "$QUEUE/$1.rc"
	} &
	echo "$!" > "$QUEUE/$1.pid"
}

# lane_ended reports whether a running lane is over. A lane whose process is
# gone without an exit code counts as failed, so the run cannot wait forever.
lane_ended() {
	[ -f "$QUEUE/$1.rc" ] && return 0
	kill -0 "$(cat "$QUEUE/$1.pid")" 2> /dev/null && return 1
	[ -f "$QUEUE/$1.rc" ] || echo "255 0" > "$QUEUE/$1.rc"
	return 0
}

# stop_lanes ends every running lane on Ctrl-C, waits for it, and takes the
# compose stack down when a compose lane was among them.
stop_lanes() {
	[ -n "$(echo $RUNNING)" ] || return 0
	echo ""
	echo "ci-local: stopping$RUNNING"
	compose=no
	for id in $RUNNING; do
		pid=$(cat "$QUEUE/$id.pid" 2> /dev/null) || continue
		kill -TERM -- "-$pid" 2> /dev/null || kill -TERM "$pid" 2> /dev/null
		case " $(cat "$QUEUE/$id.shares") " in *" compose "*) compose=yes ;; esac
	done
	wait
	if [ "$compose" = yes ]; then
		docker compose -f docker-compose.local.yml down > /dev/null 2>&1
	fi
	RUNNING=""
}

finish_lane() {
	read -r rc elapsed < "$QUEUE/$1.rc"
	IFS='	' read -r cost job < "$QUEUE/$1.job"
	RUNNING=$(echo " $RUNNING " | sed "s/ $1 / /")
	for share in $(cat "$QUEUE/$1.shares"); do
		HELD=$(echo "$HELD" | sed "s/ $share / /")
	done

	if [ "$rc" -eq 0 ]; then
		echo "OK    ci-local: $1 passed in ${elapsed}s"
		printf 'passed\t%s\t%s\t%s\t\n' "$1" "$elapsed" "$job" > "$QUEUE/$1.result"
	else
		echo "FAIL  ci-local: $1 failed in ${elapsed}s, exit $rc. The CI job is: $job"
		echo "      $LOGS/$1.log"
		printf 'failed\t%s\t%s\t%s\texit %s\n' "$1" "$elapsed" "$job" "$rc" > "$QUEUE/$1.result"
		FAILED=1
		[ "$KEEP_GOING" = 0 ] && STOPPED=1
	fi
}

run_queue() {
	while :; do
		for id in $RUNNING; do
			lane_ended "$id" && finish_lane "$id"
		done

		if [ "$STOPPED" = 1 ]; then
			for id in $PENDING; do
				IFS='	' read -r cost job < "$QUEUE/$id.job"
				printf 'unreached\t%s\t0\t%s\t%s\n' "$id" "$job" "$UNREACHED" > "$QUEUE/$id.result"
			done
			PENDING=""
		fi

		# A solo lane waits until no other lane is left to start, so the run
		# empties the slots once rather than before each solo lane.
		others=no
		for id in $PENDING; do
			is_solo "$(cat "$QUEUE/$id.shares")" || others=yes
		done

		waiting=""
		for id in $PENDING; do
			if can_start "$(cat "$QUEUE/$id.shares")" "$others"; then
				start_lane "$id"
			else
				waiting="$waiting $id"
			fi
		done
		PENDING=$waiting

		[ "$(words $RUNNING $PENDING)" -eq 0 ] && break
		sleep 1
	done
	wait

	# Each queued record takes the outcome of its lane, so the summary lists
	# the lanes in table order whatever order they ended in.
	awk -F'\t' -v dir="$QUEUE" '
		$1 == "queued" {
			file = dir "/" $2 ".result"
			if ((getline line < file) > 0) { print line } else { print }
			close(file)
			next
		}
		{ print }' "$TMP/results" > "$TMP/results.run"
	mv "$TMP/results.run" "$TMP/results"
}

# A lane that regenerates a file and diffs it reads the working tree. CI reads
# a clean checkout, so the run warns when the tree has uncommitted files.
DIRTY=$(git status --porcelain 2>/dev/null | wc -l | tr -d ' ')

# The race lane starts the compose stack only where there is no C compiler.
RACE_SHARES=-
[ "$HAS_CC" = yes ] || RACE_SHARES=compose

plan_lanes() {
	VERIFY="Backend / Verify generated code"
	CODES="Backend / Error codes"
	REPO="Repository / Commit, text and workflow gates"
	MATRIX="Backend / Model and firmware matrix"
	WEB_CLIENT="Web / Client SDK and library"
	WEB_EXAMPLES="Web / Example modules"

	lane commit-style  fast none          5   -     "$REPO"    '"$MAKE" commit-style-check RANGE="$RANGE"'
	lane capabilities  fast none          5   -     "$VERIFY"  '"$MAKE" capabilities-check'
	lane resource-kinds fast none         5   -     "$VERIFY"  '"$MAKE" resource-kinds-check'
	lane event-types   fast none          5   -     "$VERIFY"  '"$MAKE" event-types-check'
	lane slots         fast none          5   -     "$VERIFY"  '"$MAKE" slots-check'
	lane changelog     fast none          10  -     "$REPO"    '"$MAKE" changelog-check RANGE="$RANGE"'
	lane authz-vocab   fast none          10  -     "$VERIFY"  '"$MAKE" authz-vocabulary-check'
	lane openapi       fast none          15  -     "$VERIFY"  '"$MAKE" openapi-check'
	lane makefile      fast none          15  -     "$REPO"    '"$MAKE" makefile-check'
	lane workflows     fast none          5   -     "$REPO"    '"$MAKE" workflows-check'
	lane secrets       fast none          5   -     "$REPO"    '"$MAKE" secrets-check'
	lane deadcode      fast none          15  -     "$CODES"   '"$MAKE" deadcode-check'
	lane sdk           fast none          15  -     "$CODES"   '"$MAKE" sdk-check'
	lane device-issues fast none          20  -     "$VERIFY"  '"$MAKE" device-issues-check'
	lane extensions    fast none          20  solo  "$VERIFY"  '"$MAKE" extensions-check'
	lane wire          fast node          20  -     "$CODES"   '"$MAKE" wire-check'
	lane sdk-reference fast none          20  -     "$VERIFY"  '"$MAKE" sdk-reference-check'
	lane config-ref    fast none          20  -     "$VERIFY"  '"$MAKE" config-reference-check'
	lane device-models fast none          20  -     "$VERIFY"  '"$MAKE" device-models-check'
	lane go-version    fast none          25  -     "$VERIFY"  '"$MAKE" go-version-check'
	lane arch          fast none          20  solo  "$REPO"    '"$MAKE" arch-check'
	lane style         fast none          30  -     "$REPO"    '"$MAKE" style-check'
	lane client-sdk    fast pnpm          30  solo  "$WEB_CLIENT" '"$MAKE" client-sdk-check'
	lane error-codes   fast none          55  solo  "$CODES"   '"$MAKE" error-codes-check'
	lane lint          fast none          60  -     "Backend / Lint" '"$MAKE" lint'

	# One lane per CI unit test job, so a failure names the job to read.
	lane shard-demo    fast none          10  -     "Backend / Unit tests (internal/app demo)" '"$MAKE" test-shard SHARD=demo'
	lane shard-authz   fast none          10  -     "Backend / Unit tests (authz)"     '"$MAKE" test-shard SHARD=authz'
	lane shard-httpapi fast none          20  -     "Backend / Unit tests (httpapi)"   '"$MAKE" test-shard SHARD=httpapi'
	lane printer       fast none          20  printermodel "Backend / Unit tests (printer build)" '"$MAKE" test-printer'
	lane sdk-unit      fast none          20  -     "Backend / Unit tests (sdk)"       '(cd sdk && go test -short -count=1 ./... && go test -count=1 -run TestStreaming ./storageapi/storageapitest/)'
	# Solo: the natsbind tests of packages/fractal failed to create a JetStream
	# store beside other lanes and pass alone.
	lane packages-unit fast none          110 solo  "Backend / Unit tests (modules under packages/)" 'for mod in packages/*/go.mod; do (cd "${mod%/go.mod}" && go test -short -count=1 ./...) || exit 1; done'
	lane shard-internal fast none         45  -     "Backend / Unit tests (internal)"  '"$MAKE" test-shard SHARD=internal'
	lane shard-app     fast none          60  -     "Backend / Unit tests (internal/app *)" '"$MAKE" test-shard SHARD=app'
	lane shard-rest    fast none          90  -     "Backend / Unit tests (rest)"      '"$MAKE" test-shard SHARD=rest'

	lane api-compat    full none          10  -     "$MATRIX"  '"$MAKE" api-check'
	lane matrix        full none          20  -     "$MATRIX"  '"$MAKE" test-matrix-full'
	lane vocab         full none          20  -     "$MATRIX"  '"$MAKE" vocab-check'
	lane security      full govulncheck   20  -     "Backend / Security scan"          'govulncheck ./... && (cd sdk && govulncheck ./...)'
	lane e2e           full none          25  -     "$MATRIX"  '"$MAKE" test-e2e'
	# build and binary are one CI job in two steps. Under -j build is solo and
	# runs after binary.
	lane build         full none          30  solo  "Backend / Build"                  '"$MAKE" build-ci'
	lane embedded-app  full pnpm          45  solo  "Web / Embedded binary" '"$MAKE" embedded-app-check'
	lane printer-build full pnpm          60  solo  "Web / Embedded binary" '"$MAKE" build-printer PRINTER_MODEL=MK4S'
	lane engines       full docker        50  compose "Backend / Unit tests (authz)"   '"$MAKE" test-engines'
	lane adapters      full docker        60  compose "Backend / Event sink adapters"  '"$MAKE" test-adapters'
	lane compose       full none          60  -     "$CODES"   '"$MAKE" compose-check'
	lane docs          full "node docs-deps pnpm" 60 solo "Documentation (Starlight)" '(cd docs && pnpm run build) && "$MAKE" docs-check && git diff --exit-code -- docs/src/content/docs/mqtt-asyncapi.mdx docs/src/content/docs/sse-asyncapi.mdx'
	lane example-spools full "pnpm curl chromium" 90 js "$WEB_EXAMPLES" '"$MAKE" example-spools-check'
	lane journey       full "pnpm curl chromium" 90 js "Web / Journey" '"$MAKE" journey-check'
	lane oidc-providers full "docker pnpm curl chromium" 150 solo "Web / Sign-in at *" '"$MAKE" oidc-providers-check'
	lane sdk-install   full "docker pnpm" 95  "compose js" "Backend / Client packages through a registry" '"$MAKE" client-sdk-install-check'
	lane frontend      full "pnpm frontend-deps" 150 js "Web / Application" '"$MAKE" frontend-check'
	lane minimal       full none          175 -     "Backend / Unit tests (minimal build, *)" '"$MAKE" test-minimal'
	lane binary        full none          215 printermodel "Backend / Build"            '"$MAKE" binary-check'
	lane client-ui-types full pnpm        20  js    "$WEB_CLIENT" '"$MAKE" client-ui-svelte-check'
	lane client-ui-story full pnpm        15  js    "$WEB_CLIENT" '"$MAKE" client-ui-storybook'
	lane frontend-story full "pnpm frontend-deps" 20 js "Web / Application" '"$MAKE" frontend-storybook'
	lane design        full "pnpm frontend-deps" 20 js "Web / Application" '"$MAKE" design-check'
	lane client-ui-lint full pnpm         50  js    "Web / Component library lint" '"$MAKE" client-ui-lint'
	lane client-ui-test full "pnpm chromium" 150 js "Web / Component library tests" '"$MAKE" client-ui-test'
	lane docker        full docker        240 compose "Docker / Hardened image"        '"$MAKE" docker-check'
	lane race          full race-engine   600 "$RACE_SHARES" "Backend / Race"          '"$MAKE" test-race'
}

# Printed on every run, including a run where every lane passed.
print_standing_gaps() {
	echo ""
	echo "No run on this machine settles these, whatever the lanes above say:"
	echo ""
	echo "  Linux behaviour"
	echo "      Every runner is ubuntu-24.04. A path separator, a line ending, a"
	echo "      case-sensitive file name and a file mode each read differently"
	echo "      here, so a test that only fails on Linux passes here. To ask one"
	echo "      package the same question, run it in the compose stack:"
	echo "      make test-race-linux PACKAGES=./test/e2e/"
	echo ""
	echo "  Timing under load"
	echo "      A runner is four cores running the whole suite at once. A timing"
	echo "      assumption that holds on an idle developer machine can fail"
	echo "      there. The repair is the missing wait, never a wider budget."
	echo ""
	echo "  The race detector"
	echo "      The unit test jobs on a pull request run without it. The"
	echo "      Backend / Race jobs run every unit shard, the sdk module and the"
	echo "      modules under packages/ under it on a push to main and every"
	echo "      night, so CI finds a data race after a merge. The race lane here"
	echo "      asks the same question over every module, and it needs a C"
	echo "      compiler or Docker."
	echo ""
	echo "  What moves without a commit"
	echo "      The network half of scripts/check-go-version.sh: CI runs it"
	echo "      with --refresh, which asks go.dev whether the pin is still the"
	echo "      newest release, and the go-version lane answers from"
	echo "      scripts/go-releases.tsv instead. The govulncheck database and"
	echo "      the pnpm advisory database, both fetched at run time. Each of"
	echo "      the three can turn CI red hours after a green run here, with"
	echo "      nothing in the tree having changed."
}

# Skipped and unreached lanes print one line each with the reason. Deferred and
# filtered lanes print as one list of ids, because the reason is the same.
print_not_run() {
	awk -F'\t' '$1 == "skipped" || $1 == "unreached" { printf "  %-14s %s\n      %s\n", $2, $4, $5 }' "$TMP/results"

	NAMES=$(awk -F'\t' '$1 == "deferred" { printf "%s ", $2 }' "$TMP/results")
	if [ -n "$NAMES" ]; then
		echo "  deferred, because the fast half was asked for. 'make ci-local' runs them:"
		echo "$NAMES" | fold -s -w 62 | sed 's/^/      /'
	fi

	NAMES=$(awk -F'\t' '$1 == "untouched" { printf "%s ", $2 }' "$TMP/results")
	if [ -n "$NAMES" ]; then
		echo "  untouched by this branch, so a pull request would skip their CI jobs too:"
		echo "$NAMES" | fold -s -w 62 | sed 's/^/      /'
	fi

	NAMES=$(awk -F'\t' '$1 == "filtered" { printf "%s ", $2 }' "$TMP/results")
	if [ -n "$NAMES" ]; then
		echo "  not named by --only. 'make ci-local' runs the whole set:"
		echo "$NAMES" | fold -s -w 62 | sed 's/^/      /'
	fi

	return 0
}

print_summary() {
	echo ""
	echo "===================================================================="
	echo "ci-local summary"
	echo "===================================================================="

	RAN=$(grep -c '^passed	\|^failed	' "$TMP/results" || true)
	NOTRUN=$(grep -c '^skipped	\|^deferred	\|^unreached	\|^filtered	\|^untouched	' "$TMP/results" || true)

	echo ""
	echo "Ran here ($RAN):"
	if [ "$RAN" -eq 0 ]; then
		echo "  nothing."
	else
		awk -F'\t' '$1 == "passed" || $1 == "failed" { printf "  %-6s %-14s %5ss  %s\n", toupper($1), $2, $3, $4 }' "$TMP/results"
	fi

	if [ "$JOBS" -gt 1 ] && [ "$FAILED" = 1 ]; then
		echo ""
		echo "The output of each failed lane:"
		awk -F'\t' -v logs="$LOGS" '$1 == "failed" { printf "  %-14s %s/%s.log\n", $2, logs, $2 }' "$TMP/results"
	fi

	echo ""
	echo "Did not run here ($NOTRUN):"
	if [ "$NOTRUN" -eq 0 ]; then
		echo "  every lane this script knows about ran."
	else
		print_not_run
	fi

	print_standing_gaps

	TOTAL=$(awk -F'\t' '{ s += $3 } END { print s + 0 }' "$TMP/results")

	echo ""
	if [ "$FAILED" = 1 ]; then
		echo "FAIL  ci-local: $(grep -c '^failed	' "$TMP/results" || true) lane(s) failed in ${TOTAL}s. Each is named above against its CI job."
	else
		echo "OK    ci-local: $RAN lane(s) passed in ${TOTAL}s, $NOTRUN did not run."
		echo "      That is not the same as CI passing. Read the two lists above."
	fi
	if [ "$JOBS" -gt 1 ]; then
		echo "      The run took $(( $(date +%s) - RUN_START ))s on the clock, up to $JOBS lanes at a time."
	fi
}

print_plan() {
	COST=$(awk -F'\t' '$1 == "planned" { s += $3 } END { print s + 0 }' "$TMP/results")
	FASTCOST=$(awk -F'\t' '$1 == "planned" && $5 == "fast" { s += $3 } END { print s + 0 }' "$TMP/results")
	MINUTES=$(( (COST + 59) / 60 ))
	FASTMINUTES=$(( (FASTCOST + 59) / 60 ))

	echo "ci-local: the plan"
	echo ""
	if [ "$JOBS" -gt 1 ]; then
		echo "Will run, up to $JOBS at a time and the solo lanes last (about ${MINUTES} minutes of lane time"
		echo "with a warm cache, of which the ${FASTMINUTES} minutes marked fast are what 'make ci-local-fast' runs):"
	else
		echo "Will run, in this order (about ${MINUTES} minutes on a developer machine with a warm cache,"
		echo "of which the ${FASTMINUTES} minutes marked fast are what 'make ci-local-fast' runs):"
	fi
	awk -F'\t' '$1 == "planned" { printf "  %-4s %-14s %5ss  %s\n", $5, $2, $3, $4 }' "$TMP/results"
	if [ "$JOBS" -gt 1 ]; then
		echo ""
		echo "These lanes share less with the others, as the lane table explains:"
		awk -F'\t' '$1 == "planned" { n = split($6, shares, " ")
				for (i = 1; i <= n; i++) if (shares[i] != "-") names[shares[i]] = names[shares[i]] " " $2 }
			END { for (s in names) printf "  %-14s%s\n", s, names[s] }' "$TMP/results" | sort
	fi

	NOTRUN=$(grep -c '^skipped	\|^deferred	\|^filtered	\|^untouched	' "$TMP/results" || true)
	echo ""
	echo "Will not run ($NOTRUN):"
	if [ "$NOTRUN" -eq 0 ]; then
		echo "  nothing. Every lane this script knows about can run here."
	else
		print_not_run
	fi

	print_standing_gaps
}

# The dry pass fills the results file with the plan and runs nothing, so the
# printed plan and the run come from the same lane table.
SAVED_LIST_ONLY=$LIST_ONLY
LIST_ONLY=1
plan_lanes
LIST_ONLY=$SAVED_LIST_ONLY

print_plan

if [ "$LIST_ONLY" = 1 ]; then
	exit 0
fi

# A run with no lane passes nothing, so it fails. A mistyped --only id is the
# usual cause.
PLANNED=$(grep -c '^planned	' "$TMP/results" || true)
if [ "$PLANNED" -eq 0 ]; then
	echo ""
	echo "FAIL  no lane would run, so this run proves nothing. The list above says why."
	echo "      'make ci-local-list' prints every lane id."
	exit 2
fi

echo ""
if [ "$DIRTY" -gt 0 ]; then
	echo "WARN  $DIRTY file(s) in this working tree differ from HEAD. The lanes that"
	echo "      regenerate an artefact and diff it judge the tree, and CI judges a"
	echo "      clean checkout, so those two can disagree."
fi

: > "$TMP/results"
FAILED=0
STOPPED=0
RUN_START=$(date +%s)
if [ "$JOBS" -gt 1 ]; then
	mkdir -p "$QUEUE" "$LOGS"
	set -m
	rm -f "$LOGS"/*.log
	echo "ci-local: up to $JOBS lanes at a time. Each writes its output to $LOGS/<id>.log."
fi
plan_lanes
[ "$JOBS" -gt 1 ] && run_queue
print_summary

[ "$FAILED" = 1 ] && exit 1
exit 0

}
main "$@"
