<h1>
<p align="center">
  <img src="https://github.com/user-attachments/assets/43c90f2f-1df5-46e8-adda-f485ddb8f786" alt="MetaMorph Logo" width="128">
  <br>MetaMorph
</h1>
  <p align="center">
    <strong>Batch changes with a sprinkle of AI ✨.</strong>
  </p>
</p>

## About

MetaMorph is a server-first CLI for orchestrating parallel Claude Code agents that coordinate via git. It is designed
for sustained, long-running tasks and is based on [Anthropic's C compiler blog post](https://www.anthropic.com/engineering/building-c-compiler)
where 16 agents built a 100,000-line compiler over 2 weeks. Agents run as headless Docker containers, claim tasks via
file locks, and synchronize through git push conflicts. No orchestration agent — each Claude autonomously decides what
to work on next.