package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/garyburd/redigo/redis"
	"github.com/mrjones/oauth"
)

type Config struct {
	Debug          bool
	ServerUrl      string `toml:"server_url"`
	ServerAddress  string `toml:"server_address"`
	ConsumerUrl    string `toml:"consumer_url"`
	ConsumerKey    string `toml:"consumer_key"`
	ConsumerSecret string `toml:"consumer_secret"`
	RedisAddress   string `toml:"redis_address"`
	RedisPrefix    string `toml:"redis_prefix"`
}

type Context struct {
	conf      *Config
	consumer  *oauth.Consumer
	pool      *redis.Pool
	templates map[string]*template.Template
}

func NewContext(conf *Config, consumer *oauth.Consumer, pool *redis.Pool) Context {
	templates := make(map[string]*template.Template)
	return Context{conf, consumer, pool, templates}
}

func (c *Context) Root(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/suggestor/" {
		http.NotFound(w, r)
		return
	}
	loggedIn := false
	_, err := r.Cookie("oauthtoken")
	if err == nil {
		loggedIn = true
	}

	var bmjs bytes.Buffer
	err = c.templates["bookmarklet"].Execute(&bmjs, struct {
		Url string
	}{
		c.conf.ServerUrl + "/suggestor/ve.js",
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = c.templates["root"].ExecuteTemplate(w, "root.html", struct {
		Title       string
		LoggedIn    bool
		Bookmarklet template.URL
	}{
		"Suggestor",
		loggedIn,
		template.URL(bmjs.String()),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func setToken(w http.ResponseWriter, name, token, secret string, duration time.Duration) {
	exp := time.Now().Add(duration)
	val := fmt.Sprintf("%s:%s", token, secret)
	cookie := &http.Cookie{Name: name, Value: val, Expires: exp}
	http.SetCookie(w, cookie)
}

func getToken(r *http.Request, name string) (string, string, error) {
	cookie, err := r.Cookie(name)
	if err != nil {
		return "", "", err
	}
	vals := strings.SplitN(cookie.Value, ":", 2)
	return vals[0], vals[1], nil
}

var initiateParams = map[string]string{
	"title": "Special:OAuth/initiate",
}

func (c *Context) Initiate(w http.ResponseWriter, r *http.Request) {
	requestToken, url, err := c.consumer.GetRequestTokenAndUrlWithParams("oob", initiateParams)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	setToken(w, "oauthreqtoken", requestToken.Token, requestToken.Secret, time.Hour)
	http.Redirect(w, r, url, 303)
}

var callbackParams = map[string]string{
	"title": "Special:OAuth/token",
}

func (c *Context) Callback(w http.ResponseWriter, r *http.Request) {
	token, secret, err := getToken(r, "oauthreqtoken")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	requestToken := &oauth.RequestToken{Token: token, Secret: secret}
	verificationCode := r.URL.Query().Get("oauth_verifier")
	accessToken, err := c.consumer.AuthorizeTokenWithParams(requestToken, verificationCode, callbackParams)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	setToken(w, "oauthtoken", accessToken.Token, accessToken.Secret, 30*24*time.Hour)
	http.Redirect(w, r, "/suggestor/", 303)
}

type Post struct {
	Api      string `redis:"api"`
	Wikitext string `redis:"wikitext"`
	Summary  string `redis:"summary"`
	Revid    int    `redis:"revid"`
	Pageid   int    `redis:"pageid"`
	Pagename string `redis:"pagename"`
	Approved bool   `redis:"approved"`
}

func (c *Context) Post(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Body == nil {
		http.Error(w, "No body posted.", http.StatusBadRequest)
		return
	}
	var p Post
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		str := fmt.Sprintf("Cannot decode posted json: %s", err.Error())
		http.Error(w, str, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	conn := c.pool.Get()
	defer conn.Close()
	prefix := c.conf.RedisPrefix
	uid, err := redis.Int(conn.Do("INCR", prefix+"uids"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	strUid := strconv.Itoa(uid)
	key := prefix + "edit:" + strUid
	conn.Send("MULTI")
	conn.Send(
		"HMSET", key,
		"api", p.Api,
		"wikitext", p.Wikitext,
		"summary", p.Summary,
		"revid", p.Revid,
		"pageid", p.Pageid,
		"pagename", p.Pagename,
		"approved", "0",
	)
	conn.Send("LPUSH", prefix+"edits", strUid)
	_, err = conn.Do("EXEC")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	mar, err := json.Marshal(struct {
		Status string
	}{
		"success",
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(mar)
}

func (c *Context) Pending(w http.ResponseWriter, r *http.Request) {
	pendings, err := c.ListPendingEdits()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = c.templates["pending"].ExecuteTemplate(w, "pending.html", struct {
		Title    string
		Pendings []Pending
	}{
		"Pending edits",
		pendings,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

type Pending struct {
	Uid      int
	Host     string
	Summary  string
	Pagename string
	Approved bool
}

func (c *Context) ListPendingEdits() ([]Pending, error) {
	conn := c.pool.Get()
	defer conn.Close()
	prefix := c.conf.RedisPrefix
	values, err := redis.Values(conn.Do(
		"SORT", prefix+"edits",
		"DESC",
		"GET", "#",
		"GET", prefix+"edit:*->api",
		"GET", prefix+"edit:*->summary",
		"GET", prefix+"edit:*->pagename",
		"GET", prefix+"edit:*->approved",
	))
	if err != nil {
		return nil, err
	}
	var pendings []Pending
	err = redis.ScanSlice(values, &pendings)
	for i, t := range pendings {
		// TODO: Ignore error? Should we validate when adding.
		if u, err := url.Parse(t.Host); err == nil {
			pendings[i].Host = u.Host
		}
	}
	return pendings, err
}

type RespTokens struct {
	CSRFToken string `json:"csrftoken"`
}

type RespQuery struct {
	Tokens RespTokens
}

type Resp struct {
	Query RespQuery
}

func (c *Context) Approve(w http.ResponseWriter, r *http.Request) {
	token, secret, err := getToken(r, "oauthtoken")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	accessToken := &oauth.AccessToken{Token: token, Secret: secret}
	client, err := c.consumer.MakeHttpClient(accessToken)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	conn := c.pool.Get()
	defer conn.Close()
	query := r.URL.Query()
	key := c.conf.RedisPrefix + "edit:" + query.Get("uid")
	values, err := redis.Values(conn.Do("HGETALL", key))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(values) == 0 {
		http.NotFound(w, r)
		return
	}
	var post Post
	err = redis.ScanStruct(values, &post)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if post.Approved {
		http.Error(w, "Edit already approved.", http.StatusBadRequest)
		return
	}

	req, err := http.NewRequest("GET", post.Api, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	q := url.Values{}
	q.Add("action", "query")
	q.Add("meta", "tokens")
	q.Add("type", "csrf")
	q.Add("format", "json")
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if resp.Body == nil {
		http.Error(w, "No body got.", http.StatusBadRequest)
		return
	}
	var p Resp
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		str := fmt.Sprintf("Cannot decode got json: %s", err.Error())
		http.Error(w, str, http.StatusBadRequest)
		return
	}
	defer resp.Body.Close()

	csrf := p.Query.Tokens.CSRFToken

	f := url.Values{}
	f.Add("action", "edit")
	f.Add("pageid", strconv.Itoa(post.Pageid))
	f.Add("summary", post.Summary)
	f.Add("text", post.Wikitext)
	f.Add("token", csrf)
	f.Add("format", "json")

	req, err = http.NewRequest("POST", post.Api, strings.NewReader(f.Encode()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err = client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// FIXME: Parse response for better assurance edit was accepted.

	_, err = conn.Do("HSET", key, "approved", "1")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/suggestor/pending", 303)
}

type Resp2Revision struct {
	Diff map[string]string
}

type Resp2Page struct {
	Title     string
	Revisions []Resp2Revision
}

type Resp2Pages struct {
	Pages map[string]Resp2Page
}

type Resp2 struct {
	Query Resp2Pages
}

func (c *Context) Diff(w http.ResponseWriter, r *http.Request) {
	// FIXME: A lot of this is shared w/ Approve.  Needs refactoring.
	conn := c.pool.Get()
	defer conn.Close()
	query := r.URL.Query()
	key := c.conf.RedisPrefix + "edit:" + query.Get("uid")
	values, err := redis.Values(conn.Do("HGETALL", key))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(values) == 0 {
		http.NotFound(w, r)
		return
	}
	var post Post
	err = redis.ScanStruct(values, &post)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	f := url.Values{}
	f.Add("action", "query")
	f.Add("prop", "revisions")
	f.Add("revids", strconv.Itoa(post.Revid))
	f.Add("rvdifftotext", post.Wikitext)
	f.Add("format", "json")
	resp, err := http.PostForm(post.Api, f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var p Resp2
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		str := fmt.Sprintf("Cannot decode got json: %s", err.Error())
		http.Error(w, str, http.StatusBadRequest)
		return
	}
	defer resp.Body.Close()

	// FIXME: Test for `ok`
	diff := p.Query.Pages[strconv.Itoa(post.Pageid)]

	err = c.templates["diff"].ExecuteTemplate(w, "diff.html", struct {
		Title string
		Diff  template.HTML
	}{
		"Diff for: " + diff.Title,
		// FIXME: Test for `ok`
		template.HTML(diff.Revisions[0].Diff["*"]),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (c *Context) Vejs(w http.ResponseWriter, r *http.Request) {
	err := c.templates["ve"].Execute(w, struct {
		Url string
	}{
		c.conf.ServerUrl + "/suggestor/post",
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (c *Context) CompileTemplates(base string) {
	layout := template.Must(template.ParseFiles(path.Join(base, "layout.html")))
	for _, t := range []string{"root", "pending", "diff"} {
		clone, err := layout.Clone()
		if err != nil {
			log.Fatal(err)
		}
		c.templates[t] = template.Must(clone.ParseFiles(path.Join(base, fmt.Sprintf("%s.html", t))))
	}
	// TODO: Maybe these should be using "text/template"?
	for _, t := range []string{"ve", "bookmarklet"} {
		c.templates[t] = template.Must(template.ParseFiles(path.Join(base, t+".js")))
	}
}

func main() {
	configPath := flag.String("c", "./config.toml", "path to config file")
	flag.Parse()

	var conf Config
	if _, err := toml.DecodeFile(*configPath, &conf); err != nil {
		log.Fatal("Couldn't decode config file.")
	}

	consumer := oauth.NewConsumer(conf.ConsumerKey, conf.ConsumerSecret, oauth.ServiceProvider{
		RequestTokenUrl:   conf.ConsumerUrl + "Special:OAuth/initiate",
		AuthorizeTokenUrl: conf.ConsumerUrl + "Special:OAuth/authorize",
		AccessTokenUrl:    conf.ConsumerUrl + "Special:OAuth/token",
	})
	consumer.Debug(conf.Debug)

	pool := redis.Pool{
		MaxIdle:     3,
		IdleTimeout: 240 * time.Second,
		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", conf.RedisAddress)
			if err != nil {
				return nil, err
			}
			// if _, err := c.Do("AUTH", password); err != nil {
			// 	c.Close()
			// 	return nil, err
			// }
			return c, nil
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			if time.Since(t) < time.Minute {
				return nil
			}
			_, err := c.Do("PING")
			return err
		},
	}

	context := NewContext(&conf, consumer, &pool)
	base := filepath.Dir(*configPath)

	templates := filepath.Join(base, "templates")
	context.CompileTemplates(templates)

	public := http.Dir(filepath.Join(base, "public"))
	http.Handle("/suggestor/public/", http.StripPrefix("/suggestor/public/", http.FileServer(public)))

	http.HandleFunc("/suggestor/", context.Root)
	http.HandleFunc("/suggestor/initiate", context.Initiate)
	http.HandleFunc("/suggestor/callback", context.Callback)
	http.HandleFunc("/suggestor/post", context.Post)
	http.HandleFunc("/suggestor/pending", context.Pending)
	http.HandleFunc("/suggestor/approve", context.Approve)
	http.HandleFunc("/suggestor/diff", context.Diff)
	http.HandleFunc("/suggestor/ve.js", context.Vejs)

	serverAddress := conf.ServerAddress
	// Prefer, for tools labs
	if port := os.Getenv("PORT"); len(port) > 0 {
		serverAddress = fmt.Sprintf(":%s", port)
	}

	log.Println("Listening on", serverAddress)
	log.Fatal(http.ListenAndServe(serverAddress, nil))
}
