"""Drop `main` from a wailsjs models import once its last reference is gone.

A separate pass from fiximports.py on purpose: that script returns early when
the import is already correct, so folding this in there meant it never ran on
exactly the files that needed it. eslint's no-unused-vars is what catches this
-- svelte-check is happy either way -- so it has to run on every batch."""
import re, sys, glob
changed = []
for path in glob.glob('src/**/*.svelte', recursive=True) + glob.glob('src/**/*.ts', recursive=True):
    src = open(path).read()
    m = re.search(r"import type \{([^}]*)\} from ('[^']*wailsjs/go/models')", src)
    if not m or re.search(r'\bmain\.[A-Z]', src):
        continue
    names = [n.strip() for n in m.group(1).split(',') if n.strip()]
    if 'main' not in names:
        continue
    rest = [n for n in names if n != 'main']
    if not rest:
        continue
    src = src[:m.start()] + f"import type {{ {', '.join(rest)} }} from {m.group(2)}" + src[m.end():]
    open(path, 'w').write(src)
    changed.append(path)
print('\n'.join(changed) or 'none')
