# The unexplained push, 2026-09-16

`nx-baas` was pushed to `origin` and its remote branch
`fix/native-friend-list-envelope` pruned, by something nobody asked. This is
what was established and what now guards against a repeat. **Not attributed.**

## What happened

- 07:29:41 local — a local commit (`56319da`), deliberately left unpushed
  because the remote-branch deletion was waiting on a human.
- 07:44:22 local — `origin/main` advanced to that commit and the remote branch
  disappeared. Nobody ran it.
- The local branch had been deleted at ~07:29 as asked, so a pushing-with-prune
  operation would produce exactly this: the commit pushed and the now-absent
  branch pruned remotely.

## What the evidence rules out

- **It came from this clone.** The reflog reads `update by push`. A push from a
  different clone reaches this one as a *fetch* and says so. Whatever did it had
  this working copy.
- **Not a hook.** No `.git/hooks` entries, no `core.hooksPath`, local or global.
- **Not a scheduler.** No user crontab; the only user timers are podman
  healthchecks and image pruning. No system timer touches git.
- **Not an IDE or sync tool.** None running.
- **Not a remote actor.** GitHub's event API shows one push today, and that one
  is accounted for.

## What remains

Several AI coding agents run on this machine concurrently and in the same
working copies — three `opencode` processes (since 2026-09-11) and three
`t3code` (since 2026-09-15), alongside the session doing this work. Any of them
could hold this clone. Their tool-output logs contain no push in the window, but
they are not a complete record.

So: most likely a concurrent agent session, unproven, and not worth more
archaeology than it has already had.

## The guard

`hooks/pre-push` records every push: timestamp, the process ancestry, the refs,
and explicitly flags a deletion. It blocks nothing — the point is that the next
unexplained push is answerable instead of arguable.

It is **not** installed by cloning; git will not run a hook it has not been
given. Install it per clone:

```sh
cp hooks/pre-push .git/hooks/pre-push && chmod +x .git/hooks/pre-push
```

It appends to `.git/push-audit.log`, which is inside `.git` and so never
committed. A normal entry looks like:

```
2026-09-16T09:12:42Z  pid=…  remote=origin https://github.com/openpak/switch-nex.git
    1115782 git
    1115776 zsh
    965421 claude
    633825 t3code
    refs/heads/main -> 5110a80
```

The same hook is installed in `nx-baas`.

## Worth knowing either way

Concurrent agents sharing a working copy is the real hazard here, push or no
push: two sessions committing, rebasing or checking out in one clone will
corrupt each other's work in ways that are far harder to spot than a surprise
push. Whether that is the explanation or not, it is worth not doing.
