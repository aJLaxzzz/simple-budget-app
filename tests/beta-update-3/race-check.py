"""Run every Go test with race detection in bounded, sequential invocations.

This is an alternative for machines where the single planner invocation exceeds
30 minutes. No test is excluded: names are discovered with go test -list, partitioned
without overlap, and every partition is run with -race -count=1 -timeout=30m.
The ordinary `go test -race -count=1 -timeout=30m ./...` remains supported.
"""
import argparse
import re
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--list-only', action='store_true')
args = parser.parse_args()
packages = subprocess.check_output(['go', 'list', './...'], text=True).splitlines()
commands = []
for package in packages:
    if not package.endswith('/internal/planner'):
        commands.append(['go', 'test', '-race', '-count=1', '-timeout=30m', package])
        continue
    listing = subprocess.check_output(['go', 'test', package, '-list', '.'], text=True)
    names = [n for n in listing.splitlines() if n.startswith(('Test', 'Fuzz', 'Example'))]
    groups = [names[:len(names)//2], names[len(names)//2:]]
    assert len(set(names)) == len(names)
    assert set(groups[0]).isdisjoint(groups[1])
    assert sum(groups, []) == names
    print(f'{package}: {len(names)} tests/seeds, groups {list(map(len, groups))}', flush=True)
    for group in groups:
        if group:
            pattern = '^(' + '|'.join(re.escape(n) for n in group) + ')$'
            commands.append(['go', 'test', '-race', '-count=1', '-timeout=30m', package, '-run', pattern])
if args.list_only:
    print(f'{len(commands)} invocations; all packages/tests covered')
else:
    for command in commands:
        print('Running', command[5], flush=True)
        subprocess.run(command, check=True)
