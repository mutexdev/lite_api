"""Find the end of a run of top-level declarations in a Go file.

Walking FORWARD from a guessed line to the next `}` is what took
websocketSessionKey in US-063 and defaultState in US-069: if the guessed line is
already past the last declaration, the walk finds the NEXT function's closing
brace and swallows it.

This anchors on the last declaration that STARTS at or before the guess, then
walks from there -- so overshooting the guess can never pull in a neighbour."""
import re

def block_end(lines, lo, hi_guess):
    """1-indexed inclusive. Returns the line of the closing brace of the last
    declaration starting at or before hi_guess."""
    starts = [i for i in range(lo - 1, min(hi_guess, len(lines)))
              if re.match(r'^(func|type|const|var) ', lines[i])
              or re.match(r'^func \(', lines[i])]
    if not starts:
        raise SystemExit(f'REFUSED: no declaration between {lo} and {hi_guess}')
    last = starts[-1]
    if not lines[last].rstrip().endswith('{'):       # single-line decl
        return last + 1
    end = last
    while lines[end] != '}':
        end += 1
        if end > last + 800:
            raise SystemExit(f'REFUSED: no closing brace for {lines[last][:50]!r}')
    return end + 1

def names_in(lines, lo, hi):
    """Includes METHOD declarations, reported as (receiver).name.

    Missing them is how an *App method ended up inside the wsexec block: the
    old pattern only matched `func name(`, so `func (a *App) websocketDialer`
    was invisible to the very check meant to catch it."""
    out = []
    for l in lines[lo-1:hi]:
        m = re.match(r'(?:func|type|const|var) (\w+)', l)
        if m:
            out.append(m.group(1)); continue
        m = re.match(r'func \((?:\w+ )?\*?(\w+)\) (\w+)', l)
        if m:
            out.append(f'({m.group(1)}).{m.group(2)}')
    return out
