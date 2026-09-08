# jira-returns

Counts how many times issues were sent back to `returned`, and whose issues get sent back most.

The transition into `returned` is performed by QA, so the changelog author is **not** who the return
is counted against. Each return is attributed to the **Developer custom field** on the sub-ticket
(not the assignee — that changes hands over the flow), with the assignee as an explicitly flagged
fallback.

## Build and run

```bash
go build ./cmd/jira-returns
./jira-returns -stories PROJ-101,PROJ-102 -from 2026-08-01 -to 2026-08-31
./jira-returns -jql 'project = PROJ AND parent is not EMPTY AND updated >= -30d' -out -
```

`-out` writes the JSON report (default `report.json`, `-` for stdout); a short table always goes to
stdout, or to stderr when the JSON is on stdout.

## Configuration

All configuration is environment-only; the API token never comes from a flag and is never logged.

| Variable | Required | Meaning |
|---|---|---|
| `JIRA_BASE_URL` | yes | `https://your-site.atlassian.net` |
| `JIRA_EMAIL` | yes | Account e-mail (basic auth for Jira Cloud is email + API token) |
| `JIRA_API_TOKEN` | yes | [API token](https://id.atlassian.com/manage-profile/security/api-tokens) |
| `JIRA_DEVELOPER_FIELD_ID` | yes | e.g. `customfield_10050`; find it with `GET /rest/api/3/field` |
| `JIRA_RETURNED_STATUS_IDS` | yes¹ | Comma-separated status **ids**, e.g. `10007,10008` |
| `JIRA_RETURNED_STATUS_NAMES` | ¹ | Alternative to the above, resolved to ids at startup |
| `JIRA_CODE_REVIEW_STATUS_IDS` | no | Enables the rework-turnaround metric |
| `JIRA_ATTRIBUTION_MODE` | no | `current` (default) or `at_transition` |
| `JIRA_CONCURRENCY` | no | Worker-pool size for changelog fetches (default 5) |
| `JIRA_TIMEOUT` | no | Per-request timeout, e.g. `90s` (default 60s) |
| `LOG_LEVEL` | no | `debug` \| `info` \| `warn` \| `error` |

¹ One of ids or names is required. Ids win when both are set.

Finding your status ids:

```bash
curl -s -u "$JIRA_EMAIL:$JIRA_API_TOKEN" "$JIRA_BASE_URL/rest/api/3/status" \
  | python3 -c 'import json,sys; [print(s["id"], s["name"]) for s in json.load(sys.stdin)]'
```

## Attribution modes

- `current` — attribute every return to the Developer field's value today. Simpler, and right as
  long as the field does not change hands.
- `at_transition` — reconstruct the field's value at the moment of each return by replaying the
  changelog backwards from the current value. Use this when tickets get handed between developers;
  a ticket returned twice under two owners then splits one return each.

## Output

`report.json` contains `totals`, `developers` (with the 0/1/2/3+ distribution, because the average
hides the tail), `stories`, `issues` (every return with its timestamp, the QA engineer who made it,
and the developer it is attributed to) and `warnings`. Story and sub-ticket keys are included
throughout so any number can be spot-checked by hand in Jira.

Partial failures never abort the run: a per-issue error becomes a `warnings` entry.
