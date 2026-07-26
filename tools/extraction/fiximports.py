"""Add `types` to a file's wailsjs models import when it references types.X.

Handles the two shapes that occur: an existing `import type { main } from ...`
gets `types` added; a file with no models import at all gets one, with the
relative depth computed from its own path."""
import re, sys, os
changed = []
for path in sys.argv[1:]:
    src = open(path).read()
    if not re.search(r'\btypes\.[A-Z]', src):
        continue
    m = re.search(r"import type \{([^}]*)\} from ('[^']*wailsjs/go/models')", src)
    if m:
        names = [n.strip() for n in m.group(1).split(',') if n.strip()]
        if 'types' in names:
            continue
        names.append('types')
        src = src[:m.start()] + f"import type {{ {', '.join(names)} }} from {m.group(2)}" + src[m.end():]
    else:
        depth = len(os.path.relpath(path, 'src').split(os.sep)) - 1
        rel = '../' * depth + 'wailsjs/go/models'
        anchor = re.search(r'^\s*import .*$', src, re.M)
        if not anchor:
            sys.exit(f'REFUSED: no import block in {path}')
        line = f"\n  import type {{ types }} from '{rel}'"
        src = src[:anchor.end()] + line + src[anchor.end():]
    # Drop `main` once its last reference is gone. svelte-check is happy either
    # way; eslint's no-unused-vars is what catches this, so it has to be part of
    # the same pass or every batch leaves a trail of dead imports.
    m2 = re.search(r"import type \{([^}]*)\} from ('[^']*wailsjs/go/models')", src)
    if m2 and not re.search(r'\bmain\.[A-Z]', src):
        names2 = [n.strip() for n in m2.group(1).split(',') if n.strip() and n.strip() != 'main']
        if names2:
            src = src[:m2.start()] + f"import type {{ {', '.join(names2)} }} from {m2.group(2)}" + src[m2.end():]
    open(path, 'w').write(src)
    changed.append(path)
print('\n'.join(changed) or 'none')
