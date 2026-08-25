#!/bin/sh
# A Feat tracker command for GitHub Issues.
#
# Prints the open issues assigned to you in one repository, mapped onto
# schema/feat-tickets.schema.json. github-issues.output.json beside this file is
# what one run printed.
#
# Needs: gh, authenticated as you (`gh auth status`). No jq: gh embeds one.
#
# Tickets are often filed where no code lives. The repository below is a
# planning repository in that case, and Feat never has to be told it exists:
# only the command reads it.
set -eu

# The repository the issues are filed in.
REPO="acme/planning"
export REPO

# --assignee "@me" is what makes them yours. Feat passes no filter of its own,
# so this line is the whole of that decision: change it to a label, a milestone,
# or a project field and Feat neither knows nor cares.
gh issue list \
	--repo "$REPO" \
	--assignee "@me" \
	--state open \
	--limit 50 \
	--json number,title,body,url,state \
	--jq 'map({
		reference: "\(env.REPO)#\(.number)",
		title,
		body,
		url,
		state
	})'
