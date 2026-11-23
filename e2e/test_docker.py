"""Sessions whose tools run in sandboxes the controller launches with the Docker adapter."""

import contextlib
import os
import subprocess
import time

import anthropic

from conftest import containers, wait

MODEL = "mock/scripted"
TOOLSET = [{"type": "agent_toolset_20260401", "default_config": {"permission_policy": {"type": "always_allow"}}}]


def start(client: anthropic.Anthropic, environment, name: str):
    env, _ = environment
    agent = client.beta.agents.create(name=name, model=MODEL, tools=TOOLSET)
    session = client.beta.sessions.create(agent=agent.id, environment_id=env.id)
    client.beta.sessions.events.send(session.id, events=[{"type": "user.message", "content": [{"type": "text", "text": "go"}]}])
    return session


def events(client: anthropic.Anthropic, session) -> list:
    return list(client.beta.sessions.events.list(session.id, limit=1000))


def finished(client: anthropic.Anthropic, session):
    seen = events(client, session)
    done = seen and seen[-1].type == "session.status_idle" and seen[-1].stop_reason.type == "end_turn"
    return seen if done else None


def results(seen: list) -> list:
    return [e for e in seen if e.type == "user.tool_result"]


def text(result) -> str:
    return "".join(getattr(b, "text", "") for b in (result.content or []))


def test_a_tool_call_runs_in_a_sandbox_the_controller_launched(client, environment) -> None:
    session = start(client, environment, "e2e-tool")
    seen = wait("the turn to end", lambda: finished(client, session))
    assert "tool-ok" in text(results(seen)[0])
    assert len(containers(session.id)) == 3, "one sandbox: fetch, worker and sidecar"


def test_the_sandbox_holds_no_runtime_or_environment_key(client, environment) -> None:
    session = start(client, environment, "e2e-env")
    out = text(results(wait("the turn to end", lambda: finished(client, session)))[0])
    assert f"ANTHROPIC_SESSION_ID={session.id}" in out
    assert "sk-ant-oat01-" not in out and "ANTHROPIC_ENVIRONMENT_KEY" not in out


def test_a_killed_worker_is_answered_with_an_error_and_the_next_call_runs(client, environment) -> None:
    session = start(client, environment, "e2e-kill")
    wait("the sleep to start", lambda: any(e.type == "agent.tool_use" for e in events(client, session)))
    worker = wait("the worker to run", lambda: containers(session.id, "label=agentd.role=worker", "status=running"))
    time.sleep(3)
    subprocess.run(["docker", "kill", "--signal", "KILL", worker[0]], check=True)
    seen = wait("the turn to end", lambda: finished(client, session))
    first, second = results(seen)[:2]
    assert first.is_error, "the reaper answers the killed call with an error result"
    assert "after-kill" in text(second) and not second.is_error


@contextlib.contextmanager
def handoff_held():
    """Refuses sandboxes' connections to the controller's handoff port while open, so a kill
    inside it always lands before the item is collected."""
    host, port = os.environ["AGENTD_HANDOFF_ADDR"].rsplit(":", 1)
    rule = ["INPUT", "-d", host, "-p", "tcp", "--dport", port, "-j", "REJECT", "--reject-with", "tcp-reset"]
    subprocess.run(["sudo", "iptables", "-I", *rule], check=True)
    try:
        yield
    finally:
        subprocess.run(["sudo", "iptables", "-D", *rule], check=True)


def test_a_controller_killed_mid_handoff_ends_with_one_sandbox(client, environment) -> None:
    _, controller = environment
    with handoff_held():
        session = start(client, environment, "e2e-restart")
        wait("the sandbox to launch", lambda: containers(session.id, "label=agentd.role=fetch", "status=running"), timeout=60)
        controller.kill()
        controller.start()
    seen = wait("the turn to end", lambda: finished(client, session), timeout=int(os.environ.get("AGENTD_E2E_RESTART_WAIT", "300")))
    assert "restarted" in text(results(seen)[0])
    assert len(containers(session.id)) == 3, "exactly one sandbox"
