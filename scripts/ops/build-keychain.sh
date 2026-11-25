#!/bin/sh
set -eu
cd "$(dirname "$0")/../.."
mkdir -p build/server-ux
umask 077
xcrun swiftc -O -target "$(uname -m)-apple-macosx12.0" -framework Security -framework LocalAuthentication scripts/ops/native/Secrets.swift -o build/server-ux/budget-secrets.new
# Ad-hoc signing is NOT a substitute for an Apple-authorized provisioning profile.
codesign --force --sign - --options runtime build/server-ux/budget-secrets.new
mv build/server-ux/budget-secrets.new build/server-ux/budget-secrets
build/server-ux/budget-secrets status
