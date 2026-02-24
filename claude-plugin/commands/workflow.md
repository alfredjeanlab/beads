---
description: Show the AI-supervised issue workflow guide
---

Display the kbeads workflow for agents:

## Starting Work
```bash
kd news              # Check what others are working on
kd ready             # Find available work
kd show <id>         # Review issue details
kd claim <id>        # Claim it (sets assignee + in_progress)
```

## During Work
- Only claim ONE task at a time
- Create sub-tasks with `kd create` and link with `kd dep add`
- Use `kd update <id> --notes="..."` to record progress

## Completing Work
```bash
kd close <id> --reason="what was done"
git add <files> && git commit -m "..." && git push
```

## Decision Points
When you need human input:
```bash
kd decision create --prompt="Question?" --options='[...]'
```
