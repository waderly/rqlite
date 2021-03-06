// Command rqlite is the command-line interface for rqlite.
package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/Bowery/prompt"
	"github.com/mkideal/cli"
)

const maxRedirect = 21

type argT struct {
	cli.Helper
	Protocol    string `cli:"s,scheme" usage:"protocol scheme (http or https)" dft:"http"`
	Host        string `cli:"H,host" usage:"rqlited host address" dft:"127.0.0.1"`
	Port        uint16 `cli:"p,port" usage:"rqlited host port" dft:"4001"`
	Prefix      string `cli:"P,prefix" usage:"rqlited HTTP URL prefix" dft:"/"`
	Insecure    bool   `cli:"i,insecure" usage:"do not verify rqlited HTTPS certificate" dft:"false"`
	Credentials string `cli:"u,user" usage:"set basic auth credentials in form username:password"`
}

const cliHelp = `.help				Show this message
.indexes			Show names of all indexes
.schema				Show CREATE statements for all tables
.status				Show status and diagnostic information for connected node
.expvar				Show expvar (Go runtime) information for connected node
.tables				List names of tables
`

func main() {
	cli.SetUsageStyle(cli.ManualStyle)
	cli.Run(new(argT), func(ctx *cli.Context) error {
		argv := ctx.Argv().(*argT)
		if argv.Help {
			ctx.WriteUsage()
			return nil
		}

		prefix := fmt.Sprintf("%s:%d>", argv.Host, argv.Port)
	FOR_READ:
		for {
			line, err := prompt.Basic(prefix, false)
			if err != nil {
				return err
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var (
				index = strings.Index(line, " ")
				cmd   = line
			)
			if index >= 0 {
				cmd = line[:index]
			}
			cmd = strings.ToUpper(cmd)
			switch cmd {
			case ".TABLES":
				err = query(ctx, cmd, `SELECT name FROM sqlite_master WHERE type="table"`, argv)
			case ".INDEXES":
				err = query(ctx, cmd, `SELECT sql FROM sqlite_master WHERE type="index"`, argv)
			case ".SCHEMA":
				err = query(ctx, cmd, "SELECT sql FROM sqlite_master", argv)
			case ".STATUS":
				err = status(ctx, cmd, line, argv)
			case ".EXPVAR":
				err = expvar(ctx, cmd, line, argv)
			case ".HELP":
				err = help(ctx, cmd, line, argv)
			case ".QUIT", "QUIT", "EXIT":
				break FOR_READ
			case "SELECT":
				err = query(ctx, cmd, line, argv)
			default:
				err = execute(ctx, cmd, line, argv)
			}
			if err != nil {
				ctx.String("%s %v\n", ctx.Color().Red("ERR!"), err)
			}
		}
		ctx.String("bye~\n")
		return nil
	})
}

func makeJSONBody(line string) string {
	data, err := json.Marshal([]string{line})
	if err != nil {
		return ""
	}
	return string(data)
}

func help(ctx *cli.Context, cmd, line string, argv *argT) error {
	fmt.Printf(cliHelp)
	return nil
}

func status(ctx *cli.Context, cmd, line string, argv *argT) error {
	url := fmt.Sprintf("%s://%s:%d/status", argv.Protocol, argv.Host, argv.Port)
	return cliJSON(ctx, cmd, line, url, argv)
}

func expvar(ctx *cli.Context, cmd, line string, argv *argT) error {
	url := fmt.Sprintf("%s://%s:%d/debug/vars", argv.Protocol, argv.Host, argv.Port)
	return cliJSON(ctx, cmd, line, url, argv)
}

func sendRequest(ctx *cli.Context, urlStr string, line string, argv *argT, ret interface{}) error {
	data := makeJSONBody(line)
	url := urlStr

	client := http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: argv.Insecure},
	}}

	// Explicitly handle redirects.
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	nRedirect := 0
	for {
		req, err := http.NewRequest("POST", url, strings.NewReader(data))
		if err != nil {
			return err
		}

		if argv.Credentials != "" {
			creds := strings.Split(argv.Credentials, ":")
			if len(creds) != 2 {
				return fmt.Errorf("invalid Basic Auth credentials format")
			}
			req.SetBasicAuth(creds[0], creds[1])
		}

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("unauthorized")
		}

		// Check for redirect.
		if resp.StatusCode == http.StatusMovedPermanently {
			nRedirect++
			if nRedirect > maxRedirect {
				return fmt.Errorf("maximum leader redirect limit exceeded")
			}
			url = resp.Header["Location"][0]
			continue
		}

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		return json.Unmarshal(body, ret)
	}
}

// cliJSON fetches JSON from a URL, and displays it at the CLI.
func cliJSON(ctx *cli.Context, cmd, line, url string, argv *argT) error {
	// Recursive JSON printer.
	var pprint func(indent int, m map[string]interface{})
	pprint = func(indent int, m map[string]interface{}) {
		indentation := "  "
		for k, v := range m {
			if v == nil {
				continue
			}
			switch v.(type) {
			case map[string]interface{}:
				for i := 0; i < indent; i++ {
					fmt.Print(indentation)
				}
				fmt.Printf("%s:\n", k)
				pprint(indent+1, v.(map[string]interface{}))
			default:
				for i := 0; i < indent; i++ {
					fmt.Print(indentation)
				}
				fmt.Printf("%s: %v\n", k, v)
			}
		}
	}

	client := http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: argv.Insecure},
	}}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("unauthorized")
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	ret := make(map[string]interface{})
	if err := json.Unmarshal(body, &ret); err != nil {
		return err
	}

	// Specific key requested?
	parts := strings.Split(line, " ")
	if len(parts) >= 2 {
		ret = map[string]interface{}{parts[1]: ret[parts[1]]}
	}
	pprint(0, ret)

	return nil
}
