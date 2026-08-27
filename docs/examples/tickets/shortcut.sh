#!/bin/sh
# A Feat tracker command for Shortcut.
#
# Prints the stories you pick up work from, mapped onto
# schema/feat-tickets.schema.json. shortcut.output.json beside this file is what
# one run printed. What "you pick up" means is the block of defaults below, and
# every one of them is also a flag, so a project can hold the whole query in its
# argument vector:
#
#   tracker:
#     kind: command
#     command: ["<your-script-name>", "--mine", "--iteration", "current"]
#
# Needs: curl, jq, and SHORTCUT_API_TOKEN in the environment `feat` runs in.
# Shortcut publishes no first-party general-purpose CLI, which is why this one
# is an HTTP call rather than a tool: a command that is already authenticated is
# what Feat expects, and here you are the one authenticating it.
#
# Two endpoints search stories and they do not answer alike. This one, GET
# /search/stories, takes one query string of search operators and answers
# StorySearchResults, an object whose matches are under `data`. The other, POST
# /stories/search, takes named filter fields and answers a bare array of
# StorySlim. Reading `data` from that one is what makes jq say it cannot index
# an array with a string.
#
# What is used here is the query string, because it pages (`page_size`), it
# returns descriptions without being asked (`detail=full`), and it names a
# workflow state by its name rather than by an ID that would cost a call to
# /workflows. Shapes and parameters are from the published OpenAPI document,
# https://developer.shortcut.com/api/rest/v3/shortcut.openapi.json, and the
# operators from
# https://www.shortcut.com/help/fields-and-features/search-operators.
#
# The one filter that endpoint does not have is the iteration: there is no
# iteration operator in the reference, and the API keeps that filter on the
# other endpoint. Since every result carries `iteration_id`, --iteration narrows
# the page here instead, and says so when the page was not wide enough to be
# sure. It is the story's current iteration that is matched, not the ones it was
# carried over from, which the result keeps separately in
# `previous_iteration_ids`.
set -eu

# What this copy was installed as, which is what it calls itself in its own
# messages. Copy it under any name you like: usage and errors follow.
program=${0##*/}

# Defaults. Each is a flag as well, and the environment names are what let one
# installed copy serve two projects.
OWNER="${SHORTCUT_OWNER:-}"          # a mention name, without the @; --mine fills it in
ITERATION="${SHORTCUT_ITERATION:-}"  # IDs and the words current, previous, upcoming, all
STATE="${SHORTCUT_STATE:-}"          # a workflow state name, e.g. "Ready for Dev"
EXTRA="${SHORTCUT_QUERY:-}"          # further operators, verbatim
LIMIT="${SHORTCUT_LIMIT:-25}"
DONE=false
ARCHIVED=false
MINE=false

# Shortcut's own maximum for a page of search results.
MAX_PAGE=250

usage() {
    # The heredoc stays literal so that the operators inside it are printed as
    # written; the one line that varies is printed before it.
    printf 'Usage: %s [options]\n\n' "$program"
    cat <<'USAGE'
  --mine                  Only stories you own. Your mention name is read from
                          the token, so there is nothing to look up or paste.
  --owner NAME            Only stories owned by this mention name, without the
                          @. One knob with --mine: whichever comes last decides.
  --iteration LIST        Only stories in these iterations: a comma-separated
                          list of iteration IDs and the words current (every
                          started iteration), previous (every finished one),
                          upcoming (every unstarted one) and all. As in
                          --iteration current,previous or --iteration 10335,10336.
  --state NAME            Only stories in this workflow state, by its name, as
                          in --state "Ready for Dev".
  --include-done          Finished stories too. They are excluded otherwise.
  --archived              Archived stories. They are excluded otherwise.
  --query 'OPERATORS'     Appended to the query verbatim, which is how the rest
                          of Shortcut's vocabulary is reached: --query
                          'team:Engineering label:release !has:owner'.
  --limit N               At most N stories, 1 to 250, most recently updated
                          first. Default: 25.
  -h, --help              This.

Environment: SHORTCUT_API_TOKEN (required), and SHORTCUT_OWNER,
SHORTCUT_ITERATION, SHORTCUT_STATE, SHORTCUT_QUERY, SHORTCUT_LIMIT as defaults
for the flags.
USAGE
}

die() {
    printf '%s: %s\n' "$program" "$1" >&2
    exit 1
}

need_value() {
    [ "$2" -gt 0 ] || die "$1 needs a value. Try --help."
}

# A value reaches the query inside double quotes, so one of its own would end
# the operator early and the rest would be read as a search for words.
quotable() {
    case $2 in
    *'"'*) die "$1 cannot contain a double quote." ;;
    esac
}

while [ $# -gt 0 ]; do
    case $1 in
    --mine) MINE=true; OWNER="" ;;
    --owner) need_value "$1" $(($# - 1)); quotable "$1" "$2"; MINE=false; OWNER=$2; shift ;;
    --iteration) need_value "$1" $(($# - 1)); ITERATION=$2; shift ;;
    --state) need_value "$1" $(($# - 1)); quotable "$1" "$2"; STATE=$2; shift ;;
    --include-done) DONE=true ;;
    --archived) ARCHIVED=true ;;
    --query) need_value "$1" $(($# - 1)); EXTRA=$2; shift ;;
    --limit) need_value "$1" $(($# - 1)); LIMIT=$2; shift ;;
    -h | --help) usage; exit 0 ;;
    *) die "unknown option $1. Try --help." ;;
    esac
    shift
done

[ -n "${SHORTCUT_API_TOKEN:-}" ] || die "SHORTCUT_API_TOKEN is not set. It is read from the environment \`feat\` runs in."
case $LIMIT in
'' | *[!0-9]*) die "--limit wants a number, not \"$LIMIT\"." ;;
esac
[ "$LIMIT" -ge 1 ] && [ "$LIMIT" -le "$MAX_PAGE" ] ||
    die "--limit is between 1 and $MAX_PAGE, which is the largest page Shortcut returns."

# The token reaches curl on standard input rather than in an argument, because a
# process list is readable by everyone on the host.
credential() {
    printf 'header = "Shortcut-Token: %s"\n' "$SHORTCUT_API_TOKEN"
}

api_get() {
    credential | curl --silent --show-error --fail --config - \
        --header "Content-Type: application/json" \
        "https://api.app.shortcut.com/api/v3/$1"
}

search() {
    # detail=full is already the default and is passed anyway, because it is
    # what returns the description this maps onto `body`, and a default that
    # changed would be a puzzle rather than an error.
    credential | curl --silent --show-error --fail --config - \
        --get \
        --data-urlencode "query=$1" \
        --data-urlencode "page_size=$2" \
        --data-urlencode "detail=full" \
        "https://api.app.shortcut.com/api/v3/search/stories"
}

# is:story is the base rather than an empty string: the query is required and
# has a minimum length, so a run with every filter turned off has to ask for
# something.
query="is:story"

if [ "$MINE" = true ]; then
    # GET /member answers for whoever the token belongs to, which is the whole
    # of --mine: `owner:` wants a mention name, and it is not something the user
    # should have to find. There is no owner:me operator to use instead.
    OWNER=$(api_get member | jq -er '.mention_name') ||
        die "the token's own member record carries no mention name, and \`owner:\` is written with one."
fi
[ -z "$OWNER" ] || query="$query owner:\"$OWNER\""
[ -z "$STATE" ] || query="$query state:\"$STATE\""
[ "$DONE" = true ] || query="$query !is:done"
[ "$ARCHIVED" = false ] || query="$query is:archived"
[ -z "$EXTRA" ] || query="$query $EXTRA"

# An iteration is filtered on after the fact, so the page has to be as wide as
# Shortcut will make it rather than as wide as the user asked to see.
#
# What "current" and "previous" mean is Shortcut's own iteration status, which
# is one of three: a started iteration is current, a done one is previous, an
# unstarted one is upcoming. Several can be started at once, one per team, and
# all of those are current.
iterations=""
page=$LIMIT
if [ -n "$ITERATION" ]; then
    statuses=""
    ids=""
    # The selectors are keywords or digits, neither of which holds a space, so
    # the comma becomes one and the shell does the splitting.
    for token in $(printf '%s' "$ITERATION" | tr ',' ' '); do
        case $token in
        current) statuses="$statuses started" ;;
        previous) statuses="$statuses done" ;;
        upcoming) statuses="$statuses unstarted" ;;
        all) statuses="$statuses started done unstarted" ;;
        *[!0-9]*)
            die "--iteration takes iteration IDs and the words current, previous, upcoming and all, in a comma-separated list; \"$token\" is none of them."
            ;;
        *) ids="$ids $token" ;;
        esac
    done

    iterations="[]"
    if [ -n "$statuses" ]; then
        iterations=$(api_get iterations | jq -c --arg statuses "$statuses" '
            ($statuses | split(" ") | map(select(length > 0))) as $wanted
            | [.[] | select(.status as $status | $wanted | index($status)) | .id]')
    fi
    if [ -n "$ids" ]; then
        iterations=$(printf '%s' "$iterations" | jq -c --arg ids "$ids" '
            . + ($ids | split(" ") | map(select(length > 0) | tonumber)) | unique')
    fi

    # A word that matched no iteration — current in a workspace where none is
    # started, previous in a workspace on its first — is a misconfiguration
    # rather than an empty backlog, and worth saying out loud rather than
    # printing no tickets and leaving the user to wonder which of the two
    # happened. An ID is taken at its word: it costs a request to find out that
    # no iteration has it, and that request is only worth making for the words.
    [ "$iterations" != '[]' ] || die "no iteration matches --iteration $ITERATION."
    page=$MAX_PAGE
fi

# The response is held before it is mapped, because a failed request in the
# middle of a pipeline would leave jq to filter nothing and print nothing, and
# an empty standard output with a zero exit status is a tracker Feat has to call
# malformed rather than one that admits it failed.
results=$(search "$query" "$page")

# `total` counts the matches, `data` holds the page of them. An iteration
# filtered out of one page can only be trusted when the page held everything, so
# the case where it did not is reported rather than quietly undercounted. Feat
# ignores what a tracker command writes to standard error unless it fails.
if [ -n "$iterations" ]; then
    total=$(printf '%s' "$results" | jq -er '.total')
    if [ "$total" -gt "$page" ]; then
        printf '%s\n' "$program: $total stories match, and only the first $page were read;" \
            "  an iteration is filtered out of that page, so a story past it is not listed." \
            "  Narrow the query — --mine, --state, --query — to be sure of the iteration." >&2
    fi
fi

# Ordering is by when a story last moved rather than by Shortcut's relevance,
# which decays with time and would reshuffle a list that had not changed. The
# limit applies here as well as to the page, because a page widened for the
# iteration filter is wider than what the user asked to see.
#
# A story's workflow state is an identifier on the story rather than a word, and
# turning it into one means a second request to /workflows. What is used here
# instead is the pair of flags Shortcut keeps on every story, which is a state in
# Shortcut's own terms and needs no third call.
printf '%s' "$results" | jq \
    --argjson limit "$LIMIT" \
    --argjson iterations "${iterations:-null}" '
    .data
    | (if $iterations == null then . else map(select(.iteration_id as $i | $iterations | index($i))) end)
    | sort_by(.updated_at) | reverse | .[:$limit]
    | map({
        reference: "sc-\(.id)",
        title: .name,
        body: (.description // ""),
        url: .app_url,
        state: (if .completed then "done" elif .started then "started" else "unstarted" end)
    })'
