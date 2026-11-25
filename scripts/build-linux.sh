#!/bin/bash
# Build only. Never connects to a server or replaces ./budget.
set -euo pipefail
cd "$(dirname "$0")/.."
export GOTOOLCHAIN=go1.27.4
revision=$(git rev-parse HEAD)
stamp=$(date -u +%Y%m%dT%H%M%SZ)
release_dir="build/releases/${revision:0:12}-${stamp}"
for parent in build build/releases; do
  if [ -L "$parent" ]; then echo "Refusing symlink release directory" >&2; exit 1; fi
done
mkdir -p build/releases
mkdir "$release_dir"
GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0 go build -buildvcs=true -trimpath -ldflags='-s -w -X main.releaseProfile=production' -o "$release_dir/budget" .
python3 - "$release_dir" "$revision" <<'PY'
import hashlib,json,pathlib,subprocess,sys
root=pathlib.Path(sys.argv[1]);binary=root/'budget'
# Version information is extracted from this exact binary, not from personal files.
info=subprocess.check_output(['go','version','-m',str(binary)],text=True)
assert 'vcs.revision='+sys.argv[2] in info, 'binary revision mismatch'
modified='vcs.modified=true' in info
manifest={'application':'simple-budget-app','revision':sys.argv[2],'modified':modified,'target':'linux/amd64','go':'go1.27.4','schema_version':len(list(pathlib.Path('internal/planner/migrations').glob('*.sql'))),'embedded_frontend':True,'files':[{'name':'budget','size':binary.stat().st_size,'sha256':hashlib.sha256(binary.read_bytes()).hexdigest()}]}
(root/'release.json').write_text(json.dumps(manifest,indent=2)+'\n')
assert sorted(p.name for p in root.iterdir())==['budget','release.json']
PY
printf 'Linux release: %s\n' "$release_dir"
