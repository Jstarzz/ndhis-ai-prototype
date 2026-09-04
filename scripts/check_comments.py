from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
RULES = {
    ".py": ("#",),
    ".go": ("//", "/*", "*/"),
    ".ts": ("//", "/*", "*/"),
    ".tsx": ("//", "/*", "*/"),
    ".css": ("/*", "*/"),
}
SKIP = {"node_modules", ".git", "models", "data"}
violations = []
for path in ROOT.rglob("*"):
    if not path.is_file() or path.suffix not in RULES or any(part in SKIP for part in path.parts):
        continue
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        stripped = line.strip()
        if any(stripped.startswith(marker) for marker in RULES[path.suffix]):
            violations.append(f"{path.relative_to(ROOT)}:{number}")
if violations:
    raise SystemExit("code comments found:\n" + "\n".join(violations))
print("no code comments found")
