// The guldkorn tools locates forks with divergent commits or commits ahead of
// the original repository.
//
// Usage:
//
//    guldkorn [OPTION]...
//
// Flags:
//
//   -owner string
//         owner name (GitHub user or organization)
//   -q    suppress non-error messages
//   -repo string
//         repository name
//   -token string
//         GitHub OAuth personal access token
//   -watch
//         watch divergent forks
//
// Example:
//
//    guldkorn -owner USER -repo REPO -token ACCESS_TOKEN
//
// To create a personal access token on GitHub visit https://github.com/settings/tokens
//
// If the environment variable GULDKORN_GITHUB_TOKEN is set, the access token
// will be read from there.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/google/go-github/v32/github"
	"github.com/mewkiz/pkg/term"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
)

var (
	// dbg is a logger with the "guldkorn:" prefix which logs debug messages to
	// standard error.
	dbg = log.New(os.Stderr, term.CyanBold("guldkorn:")+" ", 0)
	// warn is a logger with the "guldkorn:" prefix which logs warning messages
	// to standard error.
	warn = log.New(os.Stderr, term.RedBold("guldkorn:")+" ", 0)
)

const guldkornTokenEnvName = "GULDKORN_GITHUB_TOKEN"

const use = `
Usage:

	guldkorn [OPTION]...

Flags:
`

const example = `
Example:

	guldkorn -owner USER -repo REPO -token ACCESS_TOKEN [-watch]

To create a personal access token on GitHub visit https://github.com/settings/tokens

If the environment variable ` + guldkornTokenEnvName + ` is set, the access token will be read from there.
`

func usage() {
	fmt.Fprintln(os.Stderr, use[1:])
	flag.PrintDefaults()
	fmt.Fprint(os.Stderr, example)
}

func main() {
	// Parse command line arguments.
	var (
		// Owner name (GitHub user or organization).
		ownerName string
		// Suppress non-error messages.
		quiet bool
		// Repository name.
		repoName string
		// GitHub OAuth personal access token.
		token string
		// Watch divergent forks.
		watch bool
	)
	flag.StringVar(&ownerName, "owner", "", "owner name (GitHub user or organization)")
	flag.BoolVar(&quiet, "q", false, "suppress non-error messages")
	flag.StringVar(&repoName, "repo", "", "repository name")
	flag.StringVar(&token, "token", "", "GitHub OAuth personal access token")
	flag.BoolVar(&watch, "watch", false, "watch divergent forks")
	flag.Usage = usage
	flag.Parse()
	// Sanity check of command line flags.
	if len(ownerName) == 0 {
		log.Println("owner name not specified; see -owner flag")
		flag.Usage()
		os.Exit(1)
	}
	if len(repoName) == 0 {
		log.Println("repository name not specified; see -repo flag")
		flag.Usage()
		os.Exit(1)
	}
	if envToken, ok := os.LookupEnv(guldkornTokenEnvName); ok {
		dbg.Printf("using OAuth token from %s environment variable", guldkornTokenEnvName)
		token = envToken
	}
	if len(token) == 0 {
		warn.Printf("OAuth token not specified; use -token flag or set %s environment variable", guldkornTokenEnvName)
	}
	// Mute debug messages if `-q` is set.
	if quiet {
		dbg.SetOutput(ioutil.Discard)
	}
	// Locate forks with divergent commits.
	if err := findInterestingForks(ownerName, repoName, token, watch); err != nil {
		log.Fatalf("%+v", err)
	}
}

// findInterestingForks locates forks with divergent commits or commits ahead of
// origin.
func findInterestingForks(ownerName, repoName, token string, watch bool) error {
	c := newClient(token)
	// Get repository info.
	repo, err := c.getRepo(ownerName, repoName)
	if err != nil {
		// This is considered an unrecoverable failure, as we need to repository
		// information to determine the branches of the original repository.
		return errors.WithStack(err)
	}
	dbg.Println("repo:", repo.Owner.GetLogin(), repo.GetName())
	repoBranches, err := c.getBranches(ownerName, repoName)
	if err != nil {
		return errors.WithStack(err)
	}
	for _, repoBranch := range repoBranches {
		dbg.Println("   branch:", repoBranch.GetName())
	}
	defaultBranch := repo.GetDefaultBranch()
	dbg.Println("   default branch:", defaultBranch)
	// Get all forks, including forks of forks, recursively.
	forks, err := c.getAllForks(ownerName, repoName)
	if err != nil {
		return errors.WithStack(err)
	}
	dbg.Println("forks:", len(forks))
	for _, repo := range forks {
		dbg.Println("fork:", repo.Owner.GetLogin(), repo.GetName())
	}
	for _, fork := range forks {
		divergent, err := c.compare(repo, repoBranches, fork)
		if err != nil {
			return errors.WithStack(err)
		}
		if watch && divergent {
			forkOwnerName := fork.Owner.GetLogin()
			forkRepoName := fork.GetName()
			dbg.Printf("watching https://github.com/%s/%s", forkOwnerName, forkRepoName)
			subscription := &github.Subscription{
				Subscribed: new(bool),
			}
			*subscription.Subscribed = true
			if _, _, err := c.client.Activity.SetRepositorySubscription(c.ctx, forkOwnerName, forkRepoName, subscription); err != nil {
				return errors.WithStack(err)
			}
		}
	}
	return nil
}

// compare compares the repository against the fork to find any branches of the
// fork that are ahead of the original repository. The boolean return reports
// whether the fork had any divergent commits as compared with the original
// repository.
func (c *Client) compare(repo *github.Repository, repoBranches []*github.Branch, fork *github.Repository) (bool, error) {
	defaultBranch := repo.GetDefaultBranch()
	repoBranchNames := make(map[string]bool)
	for _, repoBranch := range repoBranches {
		repoBranchNames[repoBranch.GetName()] = true
	}
	repoOwnerName := repo.Owner.GetLogin()
	repoRepoName := repo.GetName()
	forkOwnerName := fork.Owner.GetLogin()
	forkRepoName := fork.GetName()
	forkBranches, err := c.getBranches(forkOwnerName, forkRepoName)
	if err != nil {
		return false, errors.WithStack(err)
	}
	divergent := false
	for _, forkBranch := range forkBranches {
		compareRepoBranchName := defaultBranch
		forkBranchName := forkBranch.GetName()
		if _, ok := repoBranchNames[forkBranchName]; ok {
			compareRepoBranchName = forkBranchName
		}
		base := repoOwnerName + ":" + compareRepoBranchName
		head := forkOwnerName + ":" + forkBranchName
		comp, _, err := c.client.Repositories.CompareCommits(c.ctx, repoOwnerName, repoRepoName, base, head)
		if err != nil {
			for waitForRateLimitReset(err) {
				// try again after rate limit resets.
				comp, _, err = c.client.Repositories.CompareCommits(c.ctx, repoOwnerName, repoRepoName, base, head)
			}
			if err != nil {
				warn.Printf("unable to compare head=%s vs base=%s; %v", head, base, err)
				continue // try next branch.
			}
		}
		forkOwnerMadeCommit := false
		anonymousCommit := false
		for _, forkCommit := range comp.Commits {
			if forkCommit.Author.GetLogin() == forkOwnerName {
				forkOwnerMadeCommit = true
			}
			if len(forkCommit.Author.GetLogin()) == 0 {
				// This may happen if a commit is pushed without a user.email
				// registered with a correspoding GitHub user.
				anonymousCommit = true
			}
		}
		// TODO: figure out how to exclude commits that -- while divergent -- have been
		// merged with the original repository. This is the case when a commit is
		// rebased before merge.
		//
		// For example:
		//
		//    status: "diverged" (head=baosen:master vs base=diasurgical:master)
		//    baosen:master ahead 1 (and behind 1022) of diasurgical:master
		//    https://github.com/baosen/devilutionX/commits/master?author=baosen
		//
		// Commit `219241d8064c3610a594f0b152ac66da7d38ae46` gets the new hash
		// `c6d5dc48ffd45310e4b52c93506b1b04f713505e` after rebase.
		//
		// ref: https://github.com/diasurgical/devilutionX/pull/161
		// ref: https://github.com/diasurgical/devilutionX/pull/161/commits/219241d8064c3610a594f0b152ac66da7d38ae46

		// Print info if fork has commits ahead of original repository.
		if comp.GetAheadBy() > 0 {
			switch {
			case forkOwnerMadeCommit:
				fmt.Printf("status: %q (head=%s vs base=%s)\n", comp.GetStatus(), head, base)
				fmt.Printf("%s ahead %d (and behind %d) of %s\n", head, comp.GetAheadBy(), comp.GetBehindBy(), base)
				fmt.Printf("https://github.com/%s/%s/commits/%s?author=%s\n", forkOwnerName, forkRepoName, forkBranchName, forkOwnerName)
				fmt.Printf("https://github.com/%s/%s/compare/%s...%s:%s\n", repoOwnerName, repoRepoName, compareRepoBranchName, forkOwnerName, forkBranchName)
				fmt.Println()
				divergent = true
			case anonymousCommit:
				// Flag if anonymous commit was made (so it's easy to filter out).
				dbg.Printf("ANONYMOUS COMMIT status: %q (head=%s vs base=%s)", comp.GetStatus(), head, base)
				dbg.Printf("ANONYMOUS COMMIT %s ahead %d (and behind %d) of %s", head, comp.GetAheadBy(), comp.GetBehindBy(), base)
				dbg.Printf("ANONYMOUS COMMIT https://github.com/%s/%s/commits/%s", forkOwnerName, forkRepoName, forkBranchName)
				dbg.Printf("ANONYMOUS COMMIT https://github.com/%s/%s/compare/%s...%s:%s\n", repoOwnerName, repoRepoName, compareRepoBranchName, forkOwnerName, forkBranchName)
			default:
				// Flag if no commit was made by forkOwnerName (so it's easy to filter out).
				dbg.Printf("NO COMMIT BY FORK OWNER status: %q (head=%s vs base=%s)", comp.GetStatus(), head, base)
				dbg.Printf("NO COMMIT BY FORK OWNER %s ahead %d (and behind %d) of %s", head, comp.GetAheadBy(), comp.GetBehindBy(), base)
				dbg.Printf("NO COMMIT BY FORK OWNER https://github.com/%s/%s/commits/%s", forkOwnerName, forkRepoName, forkBranchName)
				dbg.Printf("NO COMMIT BY FORK OWNER https://github.com/%s/%s/compare/%s...%s:%s\n", repoOwnerName, repoRepoName, compareRepoBranchName, forkOwnerName, forkBranchName)
			}
		} else {
			//dbg.Printf("NOT AHEAD status: %q (head=%s vs base=%s)", comp.GetStatus(), head, base)
		}
	}
	return divergent, nil
}

// Client is an OAuth authenticated GitHub client.
type Client struct {
	ctx    context.Context
	client *github.Client
}

// newClient returns a GitHub client authenticated with the given OAuth token.
func newClient(token string) *Client {
	ctx := context.Background()
	var tc *http.Client
	// Use personal OAuth access token if specified.
	if len(token) > 0 {
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: token},
		)
		tc = oauth2.NewClient(ctx, ts)
	}
	client := github.NewClient(tc)
	return &Client{
		ctx:    ctx,
		client: client,
	}
}

// getRepo returns the repository of the given owner/repo.
func (c *Client) getRepo(ownerName, repoName string) (*github.Repository, error) {
	repo, _, err := c.client.Repositories.Get(c.ctx, ownerName, repoName)
	if err != nil {
		for waitForRateLimitReset(err) {
			// try again after rate limit resets.
			repo, _, err = c.client.Repositories.Get(c.ctx, ownerName, repoName)
		}
		if err != nil {
			// unable to handle error better, if its not rate limiting, this may be
			// due to a non-existant repository.
			return nil, errors.WithStack(err)
		}
	}
	return repo, nil
}

// getAllForks returns all forks of the given owner/repo, including forks of
// forks, recursively.
func (c *Client) getAllForks(ownerName, repoName string) ([]*github.Repository, error) {
	done := make(map[repoElem]bool)
	var allForks []*github.Repository
	q := newRepoQueue()
	elem := repoElem{
		ownerName: ownerName,
		repoName:  repoName,
	}
	q.push(elem)
	for !q.empty() {
		elem := q.pop()
		if _, ok := done[elem]; ok {
			continue
		}
		done[elem] = true
		forks, err := c.getForks(elem.ownerName, elem.repoName)
		if err != nil {
			return nil, errors.WithStack(err)
		}
		allForks = append(allForks, forks...)
		for _, fork := range forks {
			if fork.GetForksCount() > 0 {
				elem := repoElem{
					ownerName: fork.Owner.GetLogin(),
					repoName:  fork.GetName(),
				}
				q.push(elem)
				dbg.Println("fork has forks:", elem.ownerName, elem.repoName)
			}
		}
	}
	sort.Slice(allForks, func(i, j int) bool {
		return allForks[i].GetFullName() < allForks[j].GetFullName()
	})
	return allForks, nil
}

// getForks returns the forks of the given owner/repo.
func (c *Client) getForks(ownerName, repoName string) ([]*github.Repository, error) {
	opt := &github.RepositoryListForksOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	// get forks from all pages.
	var allForks []*github.Repository
	page := 1
	for {
		dbg.Println("list forks page:", page)
		forks, resp, err := c.client.Repositories.ListForks(c.ctx, ownerName, repoName, opt)
		if err != nil {
			for waitForRateLimitReset(err) {
				// try again after rate limit resets.
				forks, resp, err = c.client.Repositories.ListForks(c.ctx, ownerName, repoName, opt)
			}
			if err != nil {
				warn.Printf("unable to get forks of %s:%s (page %d); %v", ownerName, repoName, page, err)
				break // return partial results
			}
		}
		allForks = append(allForks, forks...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
		page++
	}
	sort.Slice(allForks, func(i, j int) bool {
		return allForks[i].GetFullName() < allForks[j].GetFullName()
	})
	return allForks, nil
}

// getBranches returns the branches of the given owner/repo.
func (c *Client) getBranches(ownerName, repoName string) ([]*github.Branch, error) {
	opt := &github.BranchListOptions{
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}
	// get branches from all pages.
	var allBrances []*github.Branch
	page := 1
	for {
		branches, resp, err := c.client.Repositories.ListBranches(c.ctx, ownerName, repoName, opt)
		if err != nil {
			for waitForRateLimitReset(err) {
				// try again after rate limit resets.
				branches, resp, err = c.client.Repositories.ListBranches(c.ctx, ownerName, repoName, opt)
			}
			if err != nil {
				warn.Printf("unable to get branches of %s:%s (page %d); %v", ownerName, repoName, page, err)
				break // return partial results
			}
		}
		allBrances = append(allBrances, branches...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
		page++
	}
	sort.Slice(allBrances, func(i, j int) bool {
		return allBrances[i].GetName() < allBrances[j].GetName()
	})
	return allBrances, nil
}

// getCommits returns the commits of the given owner/repo in the specified
// branch.
func (c *Client) getCommits(ownerName, repoName, branchName string) ([]*github.RepositoryCommit, error) {
	// TODO: use Since and Until? https://pkg.go.dev/github.com/google/go-github/github?tab=doc#CommitsListOptions
	opt := &github.CommitsListOptions{
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}
	// get commits from all pages.
	var allCommits []*github.RepositoryCommit
	page := 1
	for {
		commits, resp, err := c.client.Repositories.ListCommits(c.ctx, ownerName, repoName, opt)
		if err != nil {
			for waitForRateLimitReset(err) {
				// try again after rate limit resets.
				commits, resp, err = c.client.Repositories.ListCommits(c.ctx, ownerName, repoName, opt)
			}
			if err != nil {
				warn.Printf("unable to get commits of %s:%s in branch %q (page %d); %v", ownerName, repoName, branchName, page, err)
				break // return partial results
			}
		}
		allCommits = append(allCommits, commits...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
		page++
	}
	return allCommits, nil
}

// ### [ Helper functions ] ####################################################

// repoQueue is a queue of repositories.
type repoQueue struct {
	// Elements of queue. First element is at q.elems[q.pos]
	elems []repoElem
	// Current position in queue.
	pos int
}

// newRepoQueue returns a new queue of repositories.
func newRepoQueue() *repoQueue {
	return &repoQueue{}
}

// push pushes the given element to the end of the queue.
func (q *repoQueue) push(elem repoElem) {
	q.elems = append(q.elems, elem)
}

// pop pops and returns the first element of the queue.
func (q *repoQueue) pop() repoElem {
	pos := q.pos
	q.pos++
	return q.elems[pos]
}

// empty reports whether the queue is empty.
func (q *repoQueue) empty() bool {
	isempty := len(q.elems[q.pos:]) == 0
	if isempty {
		// reset queue while keeping underlying array.
		q.elems = q.elems[:0]
	}
	return isempty
}

// repoElem is a owner:repo element as used in the repository queue.
type repoElem struct {
	// Owner name (GitHub user or organization).
	ownerName string
	// Repository name.
	repoName string
}

// waitForRateLimitReset waits until the rate limit resets. The boolean return
// value indicates whether the given error is a GitHub rate limit error.
func waitForRateLimitReset(err error) bool {
	e, ok := err.(*github.RateLimitError)
	if !ok {
		return false
	}
	delta := time.Until(e.Rate.Reset.Time)
	dbg.Printf("rate limit hit; sleeping for %v before retrying", delta)
	time.Sleep(delta)
	return true
}
