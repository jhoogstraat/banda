# Contributing

Read [VISION.md](VISION.md) first: it says what Banda is for and how to decide. [PLAN.md](PLAN.md) has the decisions so far, [AGENTS.md](AGENTS.md) the layout and checks (written for coding agents, useful for people too).

1. `mise install`, then `mise run up` (see the README).
2. Make the change. A bug fix comes with an e2e test that fails before it.
3. Run `mise run test`, `pnpm -C web check` and `mise run e2e`; all must pass.
4. Update PLAN.md when you change a decision, and README.md when you change a setting.

Never run the real `claude` binary in tests; the e2e suite uses a fake agent.
