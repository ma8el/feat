#!/bin/sh
# A Feat tracker command for Shortcut.
#
# Prints the unfinished stories you own, mapped onto
# schema/feat-tickets.schema.json. shortcut.output.json beside this file is what
# one run printed.
#
# Needs: curl, jq, and SHORTCUT_API_TOKEN in the environment `feat` runs in.
# Shortcut publishes no first-party general-purpose CLI, which is why this one
# is an HTTP call rather than a tool: a command that is already authenticated is
# what Feat expects, and here you are the one authenticating it.
#
# Shortcut's endpoints for assignment and the current iteration were not
# verified when this was written. Check the search syntax against
# https://developer.shortcut.com before trusting it — the query is the script's
# concern rather than Feat's.
set -eu

# Your Shortcut mention name, as `owner:` matches it.
OWNER="you"

# A story's workflow state is an identifier on the story rather than a word, and
# turning it into one means a second request to /workflows. What is used here
# instead is the pair of flags Shortcut keeps on every story, which is a state in
# Shortcut's own terms and needs no second call.
curl --silent --show-error --fail \
	--header "Shortcut-Token: $SHORTCUT_API_TOKEN" \
	--get \
	--data-urlencode "query=owner:$OWNER !is:done" \
	--data-urlencode "page_size=25" \
	"https://api.app.shortcut.com/api/v3/search/stories" |
	jq '.data | map({
		reference: "sc-\(.id)",
		title: .name,
		body: (.description // ""),
		url: .app_url,
		state: (if .completed then "done" elif .started then "started" else "unstarted" end)
	})'
