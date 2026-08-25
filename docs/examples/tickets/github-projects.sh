#!/bin/sh
# A Feat tracker command for a GitHub Projects board.
#
# Prints the issues on one organisation-level board that are in a chosen column,
# mapped onto schema/feat-tickets.schema.json. github-projects.output.json
# beside this file is what one run printed.
#
# Needs: gh with the `project` scope (`gh auth refresh -s project`), and jq.
#
# A board is the case a per-repository tracker cannot express: it spans
# repositories, it carries its own fields, and the column a card is in is the
# state you actually work by. That column is what reaches Feat as the ticket's
# state, and the issue's own open/closed is not mentioned.
set -eu

# The board's number and the organisation or user that owns it.
PROJECT="7"
OWNER="acme"

# The board's own status field decides which cards are yours to pick up. A board
# with an iteration field can filter on that instead: the fields appear beside
# `status` under each item, named as the board names them.
COLUMN="Ready"

gh project item-list "$PROJECT" --owner "$OWNER" --format json --limit 100 |
	jq --arg column "$COLUMN" '[.items[]
		| select(.content.type == "Issue")
		| select(.status == $column)
		| {
			reference: "\(.content.repository | sub(".*/"; ""))#\(.content.number)",
			title: .content.title,
			body: (.content.body // ""),
			url: .content.url,
			state: .status
		}]'
