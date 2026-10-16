"""Pytest configuration for the Overdrive test suite."""

import pytest

pytest_plugins = []


def pytest_configure(config: pytest.Config) -> None:
    config.addinivalue_line("markers", "playwright: browser smoke tests (Playwright + Chromium)")
