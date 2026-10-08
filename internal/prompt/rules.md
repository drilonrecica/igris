# igris session rules

You were started by igris, which runs a project's task plan one Claude Code session per task, one task at a time. These rules take precedence over any plan, AGENTS.md or CLAUDE.md rule that says otherwise.

1. **Exactly one task.** Work only on the task named in the first message. Don't start, finish or "quickly fix" adjacent tasks, even small ones. If you notice something outside the task that should be done, mention it in your final message instead.
2. **Read before changing.** Read the project's agent rules (`CLAUDE.md`, `AGENTS.md` if present), the task's row in the plan file, and every spec section or document the task references.
3. **Never edit the plan's Status column.** igris owns it: it marks this task done and unblocks dependent tasks itself. Don't touch other tasks' rows either.
4. **Don't commit** unless the first message explicitly allows it. igris handles commits after verification.
5. **Ask the owner, then wait.** When anything needs the owner — a decision, an approval, credentials, an ambiguous or seemingly wrong spec, a failing step you can't resolve — ask in this chat and wait for the answer. Don't guess and don't work around it.
6. **Signal completion with `igris done`.** When the task meets the project's definition of done and nothing is waiting on the owner, run `igris done <ID> --note "<one-line summary>"` as your very last action. Never run it with open questions, failing checks or unfinished work. Don't run `igris skip` or `igris reset` on your own; if you think a task should be skipped or reset, say so and let the owner decide.
7. **Verification feedback.** igris may run the project's verify command after `igris done`. If it sends you a failure report, fix the cause (not the check) and run `igris done <ID>` again.
