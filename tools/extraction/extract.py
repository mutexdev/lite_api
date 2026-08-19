"""US-060 extractor: move type declarations from app.go into internal/types,
leaving aliases behind.

Takes TYPE NAMES, not line numbers, and derives the block itself: from the
`type <first>` line to the closing brace of `type <last>`. Line numbers were a
bad interface -- they shift after every extraction and I hit the same
off-by-one three times running. The names don't move.

Refuses unless the named types form one contiguous run, so a typo can't
silently swallow a neighbouring declaration."""
import re, sys

outfile, doc, names = sys.argv[1], sys.argv[2], sys.argv[3:]
lines = open('app.go').read().split('\n')

def decl_line(n):
    hits = [i for i, l in enumerate(lines) if re.match(rf'type {n} (struct|interface|\w)', l)]
    if len(hits) != 1:
        sys.exit(f'REFUSED: {n} matched {len(hits)} declarations')
    return hits[0]

start = decl_line(names[0])
last = decl_line(names[-1])
end = last
while lines[end] != '}':                       # closing brace of the last decl
    end += 1
    if end > last + 400:
        sys.exit(f'REFUSED: no closing brace for {names[-1]}')

block = lines[start:end+1]
found = [m.group(1) for l in block if (m := re.match(r'type (\w+) ', l))]
if found != names:
    sys.exit(f'REFUSED: block holds {found}, not the requested {names} -- not contiguous')

open(outfile, 'w').write(f'''// {doc}
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in package main are a Go shim and not a Wails one.
package types

''' + '\n'.join(block) + '\n')

lines[start:end+1] = ['// Moved to internal/types. Aliased so package main compiles unchanged.'] + \
                     [f'type {n} = types.{n}' for n in names]
open('app.go', 'w').write('\n'.join(lines))
# A moved struct can reference time, json and so on; goimports resolves what
# the new file now needs rather than the extractor trying to predict it.
import subprocess
for f in (outfile, 'app.go'):
    subprocess.run(['goimports', '-w', f], check=False)
    subprocess.run(['gofmt', '-w', f], check=False)
print(f'moved {len(names)}: {" ".join(names)}')
