"""Pytest configuration for the Overdrive test suite."""

import os
import re
import signal
import subprocess
import tempfile

import pytest

pytest_plugins = []


def pytest_configure(config: pytest.Config) -> None:
    config.addinivalue_line("markers", "playwright: browser smoke tests (Playwright + Chromium)")


@pytest.fixture(scope="session", autouse=True)
def stop_test_session_hosts():
    """Stop the terminal hosts the tests left. The dashboard opens terminals on load, and a Core keeps live
    terminal hosts on purpose, so stopping a test's Core leaves them running. Only hosts whose home is in the
    temporary directory are stopped; a real home is never touched."""
    yield
    if os.name != "posix":
        return
    tmp = os.path.realpath(tempfile.gettempdir()) + os.sep
    listing = subprocess.run(["ps", "-eww", "-o", "pid=,args="], capture_output=True, text=True).stdout
    for line in listing.splitlines():
        pid, _, args = line.strip().partition(" ")
        home = re.search(r"\bsession-host\b.*--home (\S+)", args)
        if home and os.path.realpath(home.group(1)).startswith(tmp):
            try:
                os.kill(int(pid), signal.SIGTERM)
            except (ProcessLookupError, PermissionError, ValueError):
                pass
