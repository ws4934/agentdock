#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/agentdock-management.XXXXXX")"
FIXTURE_PID=""
cleanup() {
  if [[ -n "$FIXTURE_PID" ]]; then kill "$FIXTURE_PID" 2>/dev/null || true; wait "$FIXTURE_PID" 2>/dev/null || true; fi
  rm -rf "$TMP_ROOT"
}
trap cleanup EXIT
SOURCE="$ROOT_DIR/desktop/macos/AgentDockApp/Sources"
TESTS="$ROOT_DIR/desktop/macos/AgentDockApp/Tests"
swiftc -swift-version 5 -parse-as-library "$SOURCE/Localization.swift" "$SOURCE/ServiceLifecycle.swift" "$TESTS/ServiceLifecycleTests.swift" -o "$TMP_ROOT/lifecycle"
"$TMP_ROOT/lifecycle"
swiftc -swift-version 5 -parse-as-library "$SOURCE/Localization.swift" "$SOURCE/TaskCenterModel.swift" "$TESTS/TaskCenterModelTests.swift" -o "$TMP_ROOT/task-model"
"$TMP_ROOT/task-model"

sources=()
for path in "$SOURCE"/*.swift; do [[ "$(basename "$path")" == main.swift ]] || sources+=("$path"); done
swiftc -swift-version 5 -parse-as-library "${sources[@]}" "$TESTS/LocalRuntimeClientTests.swift" -o "$TMP_ROOT/local-http"
python3 "$TESTS/local_runtime_fixture.py" "$TMP_ROOT/port" > "$TMP_ROOT/http.log" 2>&1 &
FIXTURE_PID=$!
for attempt in {1..100}; do [[ -s "$TMP_ROOT/port" ]] && break; sleep 0.05; done
[[ -s "$TMP_ROOT/port" ]]
"$TMP_ROOT/local-http" "$(cat "$TMP_ROOT/port")"

APP="$TMP_ROOT/LayoutFixture.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp -R "$ROOT_DIR/desktop/macos/AgentDockApp/Resources/en.lproj" "$ROOT_DIR/desktop/macos/AgentDockApp/Resources/zh-Hans.lproj" "$APP/Contents/Resources/"
cat > "$APP/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.uvwt.agentdock.layout-test</string><key>CFBundleExecutable</key><string>LayoutFixture</string><key>CFBundleDevelopmentRegion</key><string>en</string><key>LSUIElement</key><true/></dict></plist>
PLIST
swiftc -swift-version 5 -parse-as-library "${sources[@]}" "$TESTS/ManagementLayoutTests.swift" -o "$APP/Contents/MacOS/LayoutFixture"
"$APP/Contents/MacOS/LayoutFixture" -AppleLanguages '(zh-Hans)'
