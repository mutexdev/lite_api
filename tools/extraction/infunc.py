"""Apply a control edit INSIDE one function, not the first match in the file.

A whole-file s/// hits the first occurrence, which in a 17k-line file is
routinely a different function. The edit lands, it compiles, and the control
reports "0 failing" — which reads as a gap in the tests rather than a control
that never touched the code under test.
"""
import re, sys
path, func, old, new = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
src = open(path).read()
start = src.index("\nfunc " + func + "(") + 1
end = src.index("\n}\n", start) + 3
body = src[start:end]
if old not in body:
    sys.exit("PATTERN NOT IN " + func)
open(path, "w").write(src[:start] + body.replace(old, new, 1) + src[end:])
