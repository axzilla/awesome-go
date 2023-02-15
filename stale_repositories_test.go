package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/oauth2"
)

const issueTemplateContent = `
{{range .}}
- [ ] {{.}}
{{end}}
`

var issueTemplate = template.Must(template.New("issue").Parse(issueTemplateContent))

// FIXME: replace to official github client
var reGithubRepo = regexp.MustCompile("https://github.com/[a-zA-Z0-9-._]+/[a-zA-Z0-9-._]+$")
var githubGETREPO = "https://api.github.com/repos%s"
var githubGETCOMMITS = "https://api.github.com/repos%s/commits"
var githubPOSTISSUES = "https://api.github.com/repos/avelino/awesome-go/issues"

// FIXME: use https
var awesomeGoGETISSUES = "http://api.github.com/repos/avelino/awesome-go/issues" //only returns open issues
// FIXME: time.Hour * ...
var numberOfYears time.Duration = 1
var timeNow = time.Now()
var issueTitle = fmt.Sprintf("Investigate repositories with more than 1 year without update - %s", timeNow.Format("2006-01-02"))

const deadLinkMessage = " this repository might no longer exist! (status code >= 400 returned)"
const movedPermanently = " status code 301 received"
const status302 = " status code 302 received"
const archived = " repository has been archived"

// LIMIT specifies the max number of repositories that are added in a single run of the script
var LIMIT = 10
var ctr = 0

type tokenSource struct {
	AccessToken string
}

type issue struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type repo struct {
	Archived bool `json:"archived"`
}

func (t *tokenSource) Token() (*oauth2.Token, error) {
	token := &oauth2.Token{
		AccessToken: t.AccessToken,
	}
	return token, nil
}

func getRepositoriesFromBody(body string) []string {
	links := strings.Split(body, "- ")
	for idx, link := range links {
		str := strings.ReplaceAll(link, "\r", "")
		str = strings.ReplaceAll(str, "[ ]", "")
		str = strings.ReplaceAll(str, "[x]", "")
		str = strings.ReplaceAll(str, " ", "")
		str = strings.ReplaceAll(str, "\n", "")
		str = strings.ReplaceAll(str, deadLinkMessage, "")
		str = strings.ReplaceAll(str, movedPermanently, "")
		str = strings.ReplaceAll(str, status302, "")
		str = strings.ReplaceAll(str, archived, "")
		links[idx] = str
	}
	return links
}

func generateIssueBody(t *testing.T, repositories []string) (string, error) {
	t.Helper()

	buf := bytes.NewBuffer(nil)
	err := issueTemplate.Execute(buf, repositories)
	requireNoErr(t, err, "Failed to generate template")

	return buf.String(), nil
}

func createIssue(t *testing.T, staleRepos []string, client *http.Client) {
	t.Helper()

	if len(staleRepos) == 0 {
		log.Print("NO STALE REPOSITORIES")
		return
	}

	body, err := generateIssueBody(t, staleRepos)
	requireNoErr(t, err, "failed to generate issue body")

	newIssue := &issue{
		Title: issueTitle,
		Body:  body,
	}
	buf := new(bytes.Buffer)
	requireNoErr(t, json.NewEncoder(buf).Encode(newIssue), "failed to encode json req")

	req, err := http.NewRequest("POST", githubPOSTISSUES, buf)
	requireNoErr(t, err, "failed to create request")

	_, roundTripErr := client.Do(req)
	requireNoErr(t, roundTripErr, "failed to send request")
}

// FIXME: remove pointer from map
func getAllFlaggedRepositories(t *testing.T, client *http.Client, flaggedRepositories *map[string]bool) error {
	t.Helper()

	// FIXME: replace to http.MethodGet
	req, err := http.NewRequest("GET", awesomeGoGETISSUES, nil)
	requireNoErr(t, err, "failed to create request")

	res, err := client.Do(req)
	requireNoErr(t, err, "failed to send request")

	var target []issue
	defer res.Body.Close()

	requireNoErr(t, json.NewDecoder(res.Body).Decode(&target), "failed to unmarshal response")

	for _, i := range target {
		if i.Title == issueTitle {
			repos := getRepositoriesFromBody(i.Body)
			for _, repo := range repos {
				(*flaggedRepositories)[repo] = true
			}
		}
	}
	return nil
}

func containsOpenIssue(link string, openIssues map[string]bool) bool {
	_, ok := openIssues[link]
	return ok
}

func testRepoState(toRun bool, href string, client *http.Client, staleRepos *[]string) bool {
	if toRun {
		ownerRepo := strings.ReplaceAll(href, "https://github.com", "")
		apiCall := fmt.Sprintf(githubGETREPO, ownerRepo)
		req, err := http.NewRequest("GET", apiCall, nil)
		if err != nil {
			log.Printf("Failed at repository %s\n", href)
			return false
		}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Failed at repository %s\n", href)
			return false
		}
		defer resp.Body.Close()

		var repoResp repo
		json.NewDecoder(resp.Body).Decode(&repoResp)
		isRepoAdded := false
		if resp.StatusCode == http.StatusMovedPermanently {
			*staleRepos = append(*staleRepos, href+movedPermanently)
			log.Printf("%s returned %d", href, resp.StatusCode)
			isRepoAdded = true
		}
		if resp.StatusCode == http.StatusFound && !isRepoAdded {
			*staleRepos = append(*staleRepos, href+status302)
			log.Printf("%s returned %d", href, resp.StatusCode)
			isRepoAdded = true
		}
		if resp.StatusCode >= http.StatusBadRequest && !isRepoAdded {
			*staleRepos = append(*staleRepos, href+deadLinkMessage)
			log.Printf("%s might not exist!", href)
			isRepoAdded = true
		}
		if repoResp.Archived && !isRepoAdded {
			*staleRepos = append(*staleRepos, href+archived)
			log.Printf("%s is archived!", href)
			isRepoAdded = true
		}
		return isRepoAdded
	}
	return false
}

func testCommitAge(toRun bool, href string, client *http.Client, staleRepos *[]string) bool {
	if toRun {
		var respObj []map[string]interface{}
		since := timeNow.Add(-1 * 365 * 24 * numberOfYears * time.Hour)
		sinceQuery := since.Format(time.RFC3339)
		ownerRepo := strings.ReplaceAll(href, "https://github.com", "")
		apiCall := fmt.Sprintf(githubGETCOMMITS, ownerRepo)
		req, err := http.NewRequest("GET", apiCall, nil)
		isRepoAdded := false
		if err != nil {
			log.Printf("Failed at repository %s\n", href)
			return false
		}
		q := req.URL.Query()
		q.Add("since", sinceQuery)
		req.URL.RawQuery = q.Encode()
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Failed at repository %s\n", href)
			return false
		}
		defer resp.Body.Close()
		json.NewDecoder(resp.Body).Decode(&respObj)
		isAged := len(respObj) == 0
		if isAged {
			log.Printf("%s has not had a commit in a while", href)
			*staleRepos = append(*staleRepos, href)
			isRepoAdded = true
		}
		return isRepoAdded
	}
	return false
}

func TestStaleRepository(t *testing.T) {
	doc := goqueryFromReadme(t)
	var staleRepos []string
	oauth := os.Getenv("OAUTH_TOKEN")
	client := &http.Client{}
	if oauth == "" {
		log.Print("No oauth token found. Using unauthenticated client ...")
	} else {
		tokenSource := &tokenSource{
			AccessToken: oauth,
		}
		client = oauth2.NewClient(context.Background(), tokenSource)
	}
	addressedRepositories := make(map[string]bool)
	err := getAllFlaggedRepositories(t, client, &addressedRepositories)
	requireNoErr(t, err, "failed to get existing issues")

	doc.Find("body li > a:first-child").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		href, ok := s.Attr("href")
		if !ok {
			log.Println("expected to have href")
			return true
		}
		if ctr >= LIMIT && LIMIT != -1 {
			log.Print("Max number of issues created")
			return false
		}
		issueExists := containsOpenIssue(href, addressedRepositories)
		if issueExists {
			log.Printf("issue already exists for %s\n", href)
		} else {
			isGithubRepo := reGithubRepo.MatchString(href)
			if isGithubRepo {
				// FIXME: this is `or` expression. Probably we need `and`
				isRepoAdded := testRepoState(true, href, client, &staleRepos)
				isRepoAdded = testCommitAge(!isRepoAdded, href, client, &staleRepos)
				if isRepoAdded {
					ctr++
				}
			} else {
				log.Printf("%s non-github repo not currently handled", href)
			}
		}
		return true
	})
	createIssue(t, staleRepos, client)
}
