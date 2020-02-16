# Downstream

*Poesi för döda fiskar.*

The `downstream` tools locates forks with divergent commits (branches with commits ahead of the original repository).

## Installation

```
go get github.com/mewmew/downstream
```

## Usage

```
    downstream [OPTION]...

Flags:

  -owner string
        owner name (GitHub user or organization)
  -q    suppress non-error messages
  -repo string
        repository name
  -token string
        GitHub OAuth personal access token
```

## Examples

This example helped narrow down `250` forks across `2554` branches to `65` forks across `149` branches with divergent commits, a subset of which are presented below.

```bash
$ downstream -owner diasurgical -repo devilutionX -token ACCESS_TOKEN

status: "diverged" (head=AJenbo:roguelike vs base=diasurgical:master)
AJenbo:roguelike ahead 3 (and behind 139) of diasurgical:master
https://github.com/AJenbo/devilutionX/commits/roguelike?author=AJenbo

status: "diverged" (head=NEMadman:master vs base=diasurgical:master)
NEMadman:master ahead 1 (and behind 970) of diasurgical:master
https://github.com/NEMadman/devilutionX/commits/master?author=NEMadman

status: "diverged" (head=cain05:difficulty_rebalance vs base=diasurgical:master)
cain05:difficulty_rebalance ahead 13 (and behind 833) of diasurgical:master
https://github.com/cain05/devilutionX/commits/difficulty_rebalance?author=cain05

status: "diverged" (head=qndel:pixellight vs base=diasurgical:master)
qndel:pixellight ahead 81 (and behind 22) of diasurgical:master
https://github.com/qndel/devilutionX/commits/pixellight?author=qndel

...
```

**Note:** Remember to set `ACCESS_TOKEN` to not hit the rate limit on GitHub. To create a personal access token on GitHub visit https://github.com/settings/tokens
