"""The e2e suite: agentd on the runner with its scripted model, the controller (`agentd-worker
scale`) with the Docker adapter, and sandboxes from our images on the network AGENTD_E2E_NETWORK.
e2e/docker.sh sets the stack up and runs this suite."""

import os
import signal
import subprocess
import time
import uuid

import anthropic
import pytest


@pytest.fixture(scope="session")
def client() -> anthropic.Anthropic:
    return anthropic.Anthropic(api_key=os.environ["AGENTD_TEST_KEY"], base_url=os.environ["AGENTD_BASE_URL"])


class Controller:
    """One controller process for the suite's environment, which a test may kill and restart."""

    def __init__(self, environment: str, key: str, log: str) -> None:
        self.env = {**os.environ, "ANTHROPIC_ENVIRONMENT_ID": environment, "ANTHROPIC_ENVIRONMENT_KEY": key,
                    "ANTHROPIC_BASE_URL": os.environ["AGENTD_BASE_URL"]}
        self.log = open(log, "a")
        self.proc = None

    def start(self) -> None:
        self.proc = subprocess.Popen([os.environ["AGENTD_WORKER_BIN"], "scale"], env=self.env, stdout=self.log, stderr=self.log)

    def kill(self) -> None:
        self.proc.send_signal(signal.SIGKILL)
        self.proc.wait()

    def stop(self) -> None:
        self.proc.terminate()
        self.proc.wait(timeout=30)


@pytest.fixture(scope="session")
def environment(client: anthropic.Anthropic):
    env = client.beta.environments.create(name=f"e2e-{uuid.uuid4().hex[:8]}", config={"type": "self_hosted"})
    key = subprocess.run(
        [os.environ["AGENTD_BIN"], "environment-keys", "create", "--org", os.environ["AGENTD_TEST_ORG"],
         "--workspace", os.environ["AGENTD_TEST_WORKSPACE"], "--environment", env.id, "--name", "e2e"],
        capture_output=True, text=True, check=True).stdout.strip()
    controller = Controller(env.id, key, os.environ.get("AGENTD_E2E_LOG", "controller.log"))
    controller.start()
    yield env, controller
    controller.stop()


def containers(session: str, *filters: str) -> list[str]:
    args = ["docker", "ps", "-a", "-q", "--filter", f"label=agentd.session={session}"]
    for f in filters:
        args += ["--filter", f]
    return subprocess.run(args, capture_output=True, text=True, check=True).stdout.split()


def wait(what: str, check, timeout: float = 240):
    deadline = time.time() + timeout
    while time.time() < deadline:
        got = check()
        if got:
            return got
        time.sleep(0.5)
    raise TimeoutError(f"{what} within {timeout}s")
