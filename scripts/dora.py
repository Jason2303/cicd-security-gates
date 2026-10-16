#!/usr/bin/env python3
"""DORA metrics for the production deploy job, written as a Markdown dashboard.

Source of truth: the "production / deploy" job in pipeline.yaml runs on main.
  success -> a successful production deployment
  failure -> a failed production deployment (includes automatic rollbacks)
  skipped / cancelled -> not a deployment (e.g. staging failed, approval rejected)
"""
import json
import os
import statistics
import urllib.request
from datetime import datetime, timedelta, timezone

API = os.environ.get("GITHUB_API_URL", "https://api.github.com")
REPO = os.environ["GITHUB_REPOSITORY"]
TOKEN = os.environ.get("GITHUB_TOKEN", "")
WORKFLOW_FILE = os.environ.get("PIPELINE_FILE", "pipeline.yaml")
PROD_JOB = os.environ.get("PROD_JOB", "production / deploy")
DAYS = int(os.environ.get("DAYS") or "30")


def get(path):
    headers = {"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"}
    if TOKEN:
        headers["Authorization"] = f"Bearer {TOKEN}"
    req = urllib.request.Request(f"{API}{path}", headers=headers)
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.load(resp)


def parse(ts):
    return datetime.fromisoformat(ts.replace("Z", "+00:00"))


def fmt(delta):
    minutes = int(delta.total_seconds() // 60)
    if minutes < 60:
        return f"{minutes} min"
    hours, minutes = divmod(minutes, 60)
    if hours < 48:
        return f"{hours} h {minutes} min"
    return f"{hours // 24} d {hours % 24} h"


def collect():
    since = (datetime.now(timezone.utc) - timedelta(days=DAYS)).date().isoformat()
    deploys, page = [], 1
    while True:
        data = get(
            f"/repos/{REPO}/actions/workflows/{WORKFLOW_FILE}/runs"
            f"?branch=main&status=completed&per_page=100&page={page}&created=%3E%3D{since}"
        )
        runs = data.get("workflow_runs", [])
        for run in runs:
            if run["event"] not in ("push", "workflow_dispatch"):
                continue
            jobs = get(f"/repos/{REPO}/actions/runs/{run['id']}/jobs?per_page=100")["jobs"]
            for job in jobs:
                if job["name"] != PROD_JOB or job["conclusion"] not in ("success", "failure"):
                    continue
                deploys.append({
                    "finished": parse(job["completed_at"]),
                    "committed": parse(run["head_commit"]["timestamp"]),
                    "sha": run["head_sha"],
                    "ok": job["conclusion"] == "success",
                    "url": run["html_url"],
                })
        if len(runs) < 100:
            break
        page += 1
    return sorted(deploys, key=lambda d: d["finished"])


def report(deploys):
    ok = [d for d in deploys if d["ok"]]
    failed = [d for d in deploys if not d["ok"]]
    weeks = DAYS / 7

    freq = f"{len(ok) / weeks:.1f} per week ({len(ok)} in {DAYS} days)"
    lead = fmt(statistics.median(d["finished"] - d["committed"] for d in ok)) if ok else "n/a"
    cfr = f"{100 * len(failed) / len(deploys):.0f}% ({len(failed)} of {len(deploys)})" if deploys else "n/a"

    restores, unrestored = [], 0
    for f in failed:
        nxt = next((d for d in ok if d["finished"] > f["finished"]), None)
        if nxt:
            restores.append(nxt["finished"] - f["finished"])
        else:
            unrestored += 1
    mttr = fmt(statistics.median(restores)) if restores else "n/a"
    if unrestored:
        mttr += f" ({unrestored} not yet followed by a good deploy)"

    lines = [
        "## DORA metrics: production",
        "",
        f"Window: last **{DAYS} days** · Repo: `{REPO}` · Generated {datetime.now(timezone.utc):%Y-%m-%d %H:%M} UTC",
        "",
        "| Metric | Value | How it's measured |",
        "|---|---|---|",
        f"| Deployment frequency | **{freq}** | successful `{PROD_JOB}` jobs |",
        f"| Lead time for changes | **{lead}** (median) | commit time → production deploy finished (includes approval wait) |",
        f"| Change failure rate | **{cfr}** | failed production deploys (incl. automatic rollbacks) ÷ all |",
        f"| Time to restore | **{mttr}** (median) | failed deploy → next successful production deploy |",
        "",
        "### Recent production deployments",
        "",
        "| Finished (UTC) | Commit | Result | Lead time |",
        "|---|---|---|---|",
    ]
    for d in reversed(deploys[-10:]):
        result = "✅ success" if d["ok"] else "❌ failure"
        lines.append(
            f"| {d['finished']:%Y-%m-%d %H:%M} | [`{d['sha'][:7]}`]({d['url']}) | {result} "
            f"| {fmt(d['finished'] - d['committed'])} |"
        )
    if not deploys:
        lines.append("| no production deployments in this window | | | |")
    return "\n".join(lines) + "\n"


def main():
    md = report(collect())
    print(md)
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a", encoding="utf-8") as fh:
            fh.write(md)


if __name__ == "__main__":
    main()
