#!/bin/sh
# A Feat tracker command for GitLab Issues.
#
# Prints the open issues assigned to you across the instance, mapped onto
# schema/feat-tickets.schema.json. gitlab-issues.output.json beside this file is
# what one run printed.
#
# Needs: glab, authenticated as you (`glab auth status`), and jq.
#
# It asks the instance rather than a repository, because `scope=assigned_to_me`
# is answered across every project you can see. A self-hosted instance needs no
# change here: glab reads its host from your own configuration.
set -eu

# state=opened is GitLab's word, not Feat's. Feat carries what the tracker says
# a state is and maps it onto no vocabulary of its own.
#
# --paginate prints one JSON array per page, so the filter slurps them together
# rather than mapping the first page and dropping the rest.
glab api --paginate 'issues?scope=assigned_to_me&state=opened&per_page=50' |
	jq --slurp '(add // []) | map({
		reference: .references.full,
		title: .title,
		body: (.description // ""),
		url: .web_url,
		state: .state
	})'
