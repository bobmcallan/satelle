# sty_7d098d50 AC13 dogfood

cursor-agent 2026.10.01-e373342; satelle 0.0.635+a5f4b03a55fb-dirty (commit a5f4b03a55fb, built 2026-10-10-07-40-52) — global; repo /SCRATCH/df/repo-1791624060

Installed .cursor/hooks.json (unmodified):
```
{
  "version": 1,
  "hooks": {
    "preToolUse": [
      {
        "command": "sh /SCRATCH/df/repo-1791624060/.satelle/hooks/satelle-hook.sh gate cursor"
      },
      {
        "command": "sh /SCRATCH/df/repo-1791624060/.satelle/hooks/satelle-hook.sh commitgate cursor"
      }
    ],
    "sessionStart": [
      {
        "command": "PATH=$HOME/.local/bin:$PATH satelle hook context --harness cursor"
      }
    ],
    "stop": [
      {
        "command": "PATH=$HOME/.local/bin:$PATH satelle hook stopcheck --harness cursor"
      }
    ]
  }
}
```

[context] other harness wiring before run: absent .claude/settings.json; absent .grok/hooks/satelle.json; absent .pi/extensions/satelle.ts
[context] reply: '# Always-resident principles (satelle)'
[context] PASS=True

[stop] other harness wiring before run: absent .claude/settings.json; absent .grok/hooks/satelle.json; absent .pi/extensions/satelle.ts
[stop] tty contains STOP BLOCKED: True; UNGATED: True; HI count: 8
[stop] PASS=True

[refuse-write] other harness wiring before run: absent .claude/settings.json; absent .grok/hooks/satelle.json; absent .pi/extensions/satelle.ts
[refuse-write] fs: README.md: line one|line two|; other.txt: PRESENT; rejections: [('editToolCall', "satelle: you're mutating the tree without a performing story, or you have used the wrong tool for reading. Open a story session before editing code: satelle sto")]
[refuse-write] PASS=True

[refuse-delete] other harness wiring before run: absent .claude/settings.json; absent .grok/hooks/satelle.json; absent .pi/extensions/satelle.ts
[refuse-delete] fs: README.md: line one|line two|; other.txt: PRESENT; rejections: [('deleteToolCall', "satelle: you're mutating the tree without a performing story, or you have used the wrong tool for reading. Open a story session before editing code: satelle sto")]
[refuse-delete] PASS=True

[refuse-shell-rm] other harness wiring before run: absent .claude/settings.json; absent .grok/hooks/satelle.json; absent .pi/extensions/satelle.ts
[refuse-shell-rm] fs: README.md: line one|line two|; other.txt: PRESENT; rejections: [('shellToolCall', "satelle: you're mutating the tree without a performing story, or you have used the wrong tool for reading. Open a story session before editing code: satelle sto")]
[refuse-shell-rm] PASS=True

[refuse-git-commit] other harness wiring before run: absent .claude/settings.json; absent .grok/hooks/satelle.json; absent .pi/extensions/satelle.ts
[refuse-git-commit] fs: README.md: line one|line two|; other.txt: PRESENT; rejections: [('shellToolCall', 'satelle: refusing to commit/push with no engaged story. This gate runs BEFORE the command executes — an engage line inside the same tool call cannot pass it. En')]
[refuse-git-commit] PASS=True

[engaged] seats before engaging: []
[engaged] SATELLE_SESSION=dogfood-cursor-sty_7d098d50 for story set and cursor-agent
[engaged] story sty_f39622df status=in_progress
[engaged-write] other harness wiring before run: absent .claude/settings.json; absent .grok/hooks/satelle.json; absent .pi/extensions/satelle.ts
[engaged-write] fs: README.md: line one|line TWO|; other.txt: PRESENT; rejections: []
[engaged-write] PASS=True

## Results
- context: PASS
- stop: PASS
- refuse-write: PASS
- refuse-delete: PASS
- refuse-shell-rm: PASS
- refuse-git-commit: PASS
- engaged-write: PASS
