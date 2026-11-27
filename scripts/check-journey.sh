#!/bin/sh
# The customer journey, end to end, against a core that is really running.
#
# The journey: sign in, upload a print file on the storage page, see the
# picture the slicer stored in it and what it said about the print, press Print
# on that row, pick a printer, confirm, land on that printer's page and watch
# the print being tracked.
#
# What it does, in order:
#
#   1. builds the core and the demo
#   2. writes a G-code file that carries a real PNG, in the comment form
#      PrusaSlicer writes a stored picture in
#   3. starts the demo with simulated printers and a storage on disk
#   4. signs in with the account the demo printed
#   5. finds a printer that is connected, free and takes a print command
#   6. drives the whole journey in a real Chromium, over the application, and
#      reads the core back after the print to prove the machine took it
#   7. opens ten tabs of one dashboard, on a host name that is not a secure
#      context, and proves the tabs share one event stream
#   8. drops a file on a printer page, leaves the page and reloads it while
#      the printer is fetching the file, and proves the file lands whole
#   9. signs in two more people, and proves the file on its way shows in the
#      header of the one who may read the printer and not of the one who may
#      not
#
# The file is written here and not read out of internal/printfile/testdata.
# PrusaSlicer renders a picture in its window and not on the command line, so
# no file in that folder carries one, and the journey is about the picture a
# person sees before they press Print.
#
# Needs go, pnpm, curl and a Playwright Chromium.
#
# The whole run is one function, called on the last line, so the shell parses
# this file to its end before executing any of it. sh reads a script while it
# runs, and an edit landing mid-run moves the read offset under the reader.
main() {

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT" || exit 1

APP_DIR=frontend
LIBRARY_DIR=packages/svelte
CLIENT_SDK_DIR=packages/typescript
FRACTAL_CLIENT_DIR=packages/fractal/client

# Everything this run writes goes under bin/, which git ignores.
WORK_ROOT=bin/journey
DEMO_DIR=$WORK_ROOT/demo

# Not 8080, and not the address of the other lanes here.
DEMO_ADDR=127.0.0.1:18096
BASE_URL=http://$DEMO_ADDR

# The storage the demo declares first, and the file the journey prints.
STORAGE=local
PRINT_FILE=journey-cube.gcode

CI=true
export CI

for tool in go pnpm node curl; do
	if ! command -v "$tool" > /dev/null 2>&1; then
		echo "FAIL  $tool is not on PATH, and this lane needs it."
		exit 1
	fi
done

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT INT TERM

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
mkdir -p "$DEMO_DIR"

echo "=== The customer journey ==="

# The application links to these three packages through their dist folders.
# They are built every time, because an old dist would test old code.
step "the client SDK builds" sh -c \
	"cd '$CLIENT_SDK_DIR' && pnpm install --frozen-lockfile && pnpm run build"

step "the component library builds" sh -c \
	"cd '$LIBRARY_DIR' && pnpm install --frozen-lockfile && pnpm run build"

step "the Fractal module's client builds" sh -c \
	"cd '$FRACTAL_CLIENT_DIR' && pnpm install --frozen-lockfile && pnpm run build"

# The application's svelte-check covers its Storybook config, which imports
# the design CLI.
step "the application installs" sh -c \
	"cd '$APP_DIR' && pnpm install --frozen-lockfile && pnpm --dir ../tools/design install --frozen-lockfile"

# The application is served by a Vite dev server in the lane, so it is not
# built here. It is type-checked, because a page that does not compile is a
# journey that fails for a reason the browser cannot say.
step "the application type-checks" sh -c \
	"cd '$APP_DIR' && pnpm run check:svelte"

# ── The core ─────────────────────────────────────────────────────────
CORE=$WORK_ROOT/prusa-connect$(go env GOEXE)
DEMO=$WORK_ROOT/demo-server$(go env GOEXE)

step "the core and the demo build" sh -c \
	"go build -o '$CORE' ./cmd/prusa-connect && go build -o '$DEMO' ./cmd/demo"

# ── The file the journey prints ──────────────────────────────────────
# A 96 by 64 PNG of an orange and blue chequer, wrapped the way PrusaSlicer
# wraps a stored picture: an opening line naming the size and the byte count,
# the base64 as comment lines, and a closing line. The test reads the size back
# off the picture the browser drew, so a file whose picture never reached the
# page fails there.
#
# The settings after the movement are the ones the print sheet draws: the time,
# the filament, the material and the model.
#
# It is written under bin/ and not in the shell's temporary directory. The
# browser lane is a Node process, and a path this shell spells as /tmp/... is a
# path Node on Windows cannot open.
PICTURE_BYTES=266
GCODE=$WORK_ROOT/$PRINT_FILE

write_gcode() {
	{
		echo "; generated by scripts/check-journey.sh"
		echo "; thumbnail begin 96x64 $PICTURE_BYTES"
		cat <<- 'PICTURE'
			; iVBORw0KGgoAAAANSUhEUgAAAGAAAABACAIAAABqVuVZAAAA0UlEQVR4nOzYoQ0CURAAUY7QAB5BD9
			; RFAVRAASgcxdECHofaMBKSe0/+WzVZcdnd63HcTA63y/j+PF9XNb8dX/kQKAgUBAoCBYGCQGHZn+7j
			; h3/7H/nVvA0KAgWBgkBBoCBQECgs7kHf521QECgIFAQKAgWBgkDBPSjmbVAQKAgUBAoCBYGCQME9KO
			; ZtUBAoCBQECgIFgYJAwT0o5m1QECgIFAQKAgWBgkDBPSjmbVAQKAgUBAoCBYGCQME9KOZtUBAoCBQE
			; CgIFgYJA4R0AAP//YROOxF5l8qAAAAAASUVORK5CYII=
		PICTURE
		echo "; thumbnail end"

		i=0
		while [ "$i" -lt 200 ]; do
			echo "G1 X$i Y$i E0.1"
			i=$((i + 1))
		done

		echo "; filament used [g] = 7.25"
		echo "; estimated printing time (normal mode) = 1h 2m 16s"
		echo "; filament_type = PLA"
		echo "; printer_model = MK4IS"
	} > "$GCODE"

	[ -s "$GCODE" ]
}

step "the print file is written with a picture in it" write_gcode

# ── The demo ─────────────────────────────────────────────────────────
# Two simulated Prusa printers, because the demo starts a print on the first
# one, so the second is the machine this journey has to find. No camera: a
# frame every two seconds tells this lane nothing. The printers fetch a file
# at 64 KiB a second, so the file the relay lane sends is still on its way
# when that lane reloads the page.
DEMO_PID=

start_demo() {
	"$DEMO" -dir "$DEMO_DIR" -addr "$DEMO_ADDR" -printers 3 -flap 0 \
		-cameras=false -transfer-rate 65536 -config "" >> "$WORK/demo.log" 2>&1 &
	DEMO_PID=$!
	trap 'kill "$DEMO_PID" 2> /dev/null; rm -rf "$WORK"' EXIT INT TERM
}

ready() {
	waited=0
	while [ "$waited" -lt 120 ]; do
		if curl -fsS "$BASE_URL/api/v1/health" > /dev/null 2>&1; then
			return 0
		fi

		if ! kill -0 "$DEMO_PID" 2> /dev/null; then
			echo "the demo ended before it served" >&2
			cat "$WORK/demo.log" >&2

			return 1
		fi

		waited=$((waited + 1))
		sleep 1
	done

	cat "$WORK/demo.log" >&2

	return 1
}

# The demo prints its account and a new password on every run, so the password
# is read off what it printed. It prints that line once the fleet has dialled
# in, which is after the core is answering.
read_password() {
	waited=0
	while [ "$waited" -lt 120 ]; do
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
	cat "$WORK/demo.log" >&2

	return 1
}

start_demo
step "the demo serves" ready
step "the demo prints an account to sign in with" read_password
step "the demo account signs in" sign_in

# ── The printer this journey prints on ───────────────────────────────
# Connected, not printing, declaring the start-print verb, and driven by the
# simulator. Reading it off the fleet rather than naming a serial keeps the
# lane right when the demo changes how many printers it starts or which one it
# sets printing.
#
# The driver is named because the demo's fleet holds one machine that is not a
# Prusa. That machine answers a start-print command and prints nothing, so a
# journey sent to it would have no job to watch. The simulator is the far end
# of the xBuddy dialect and is the machine that really runs a print.
SIMULATED_DRIVER=xbuddy

free_printer() {
	curl -fsS -b "$WORK/cookies" "$BASE_URL/api/v1/devices" > "$WORK/devices.json" || return 1

	node -e '
		const fleet = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"));
		const driver = process.argv[2];
		const free = ["IDLE", "READY", "FINISHED", "STOPPED"];
		const taken = fleet.items.find(
			(device) =>
				device.connected === true &&
				device.driver === driver &&
				(device.capabilities ?? []).includes("core.command.start_print") &&
				free.includes(device.state?.["/state"]),
		);
		if (taken === undefined) {
			console.error(`no ${driver} printer in the fleet is free to take a print`);
			process.exit(1);
		}
		console.log(taken.id);
		console.log(taken.name);
	' "$WORK/devices.json" "$SIMULATED_DRIVER" > "$WORK/printer.txt" || return 1

	DEVICE_ID=$(sed -n 1p "$WORK/printer.txt")
	DEVICE_NAME=$(sed -n 2p "$WORK/printer.txt")

	[ -n "$DEVICE_ID" ] && [ -n "$DEVICE_NAME" ]
}

step "a printer in the fleet is free to take a print" free_printer

echo "    the journey prints on $DEVICE_NAME ($DEVICE_ID)"

# ── The journey, in a real Chromium ──────────────────────────────────
# Everything above is the ground the journey stands on. This is the journey: a
# person on the application, doing it, with the core answering every call.
#
# Its output is not captured. The lane prints its own step for each part of the
# journey, and a run that waits in silence for three minutes says nothing about
# where it is. The path to the file is relative to the application folder,
# because that is the directory the browser lane runs in.
STEPS=$((STEPS + 1))
printf '%2d. %s...\n' "$STEPS" "the journey runs in a browser"

if ! (
	cd "$APP_DIR" &&
		PCL_CORE_URL="$BASE_URL" \
			PCL_DEMO_USER=demo \
			PCL_DEMO_PASSWORD="$PASSWORD" \
			PCL_JOURNEY_FILE="../$GCODE" \
			PCL_JOURNEY_DEVICE="$DEVICE_ID" \
			PCL_JOURNEY_DEVICE_NAME="$DEVICE_NAME" \
			PCL_JOURNEY_STORAGE="$STORAGE" \
			pnpm exec vitest run --config test/journey/vitest.config.ts
); then
	echo "FAIL  the journey did not finish"
	cat "$WORK/demo.log"
	exit 1
fi

kill "$DEMO_PID" 2> /dev/null
wait "$DEMO_PID" 2> /dev/null

echo "OK    the journey passed $STEPS steps"
echo
echo "      the file      $PRINT_FILE on the $STORAGE storage"
echo "      the printer   $DEVICE_NAME"
echo "      the demo      $DEMO_DIR"

}
main "$@"
