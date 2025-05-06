package main

import (
    "bufio"
    "context"
    "encoding/json"
    "flag"
    "fmt"
    "io"
    "log"
    "net"
    "net/http"
    "os"
    "strings"
    "sync"
    "time"

    "github.com/PuerkitoBio/goquery"
    "github.com/tomnomnom/gahttp"
)

// Constants
const version = "1.1.0"
const defaultConcurrency = 10
const defaultTimeout = 10 // seconds

// Global variables
var (
    outputFile string
    filterStr  string
    silent     bool
    verbose    bool
    format     string
    threads    int
    timeoutSec int
)

type Result struct {
    URL   string `json:"url"`
    Value string `json:"value"`
}

var results []Result
var mu sync.Mutex

func init() {
    flag.StringVar(&outputFile, "o", "", "Output file")
    flag.StringVar(&filterStr, "f", "", "Filter results by substring (e.g., .js)")
    flag.BoolVar(&silent, "s", false, "Silent mode")
    flag.BoolVar(&verbose, "v", false, "Verbose output")
    flag.StringVar(&format, "format", "text", "Output format: text or json")
    flag.IntVar(&threads, "t", defaultConcurrency, "Number of concurrent workers")
    flag.IntVar(&timeoutSec, "timeout", defaultTimeout, "Request timeout in seconds")
}

func main() {
    flag.Parse()

    args := flag.Args()
    if len(args) == 0 && len(os.Args) < 2 {
        showHelp()
        os.Exit(1)
    }

    domains := getDomainsFromArgs(args)

    if len(domains) == 0 {
        log.Fatal("No domains provided. Use -h for help.")
    }

    ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
    defer cancel()

    client := &http.Client{
        Transport: &http.Transport{
            MaxIdleConnsPerHost: 20,
            DialContext: (&net.Dialer{
                Timeout:   30 * time.Second,
                KeepAlive: 30 * time.Second,
            }).DialContext,
            TLSHandshakeTimeout:   10 * time.Second,
            ExpectContinueTimeout: 1 * time.Second,
        },
    }

    for _, domain := range domains {
        baseURL := normalizeDomain(domain)
        processDomain(ctx, baseURL)
    }

    printResults()
    writeToFile()
}

func showHelp() {
    fmt.Fprintf(os.Stderr, "Usage: [domains] | golinkfinder [flags]\n")
    fmt.Fprintf(os.Stderr, "Examples:\n")
    fmt.Fprintf(os.Stderr, "  cat domains.txt | golinkfinder\n")
    fmt.Fprintf(os.Stderr, "  golinkfinder -d https://example.com\n")
    fmt.Fprintf(os.Stderr, "Flags:\n")
    flag.PrintDefaults()
}

func normalizeDomain(url string) string {
    if !strings.HasPrefix(url, "http") {
        return "https://" + url
    }
    return url
}

func getDomainsFromArgs(args []string) []string {
    var domains []string

    for _, arg := range args {
        if strings.HasPrefix(arg, "-") {
            continue
        }
        domains = append(domains, arg)
    }

    stdinDomains, _ := readStdinLines()
    domains = append(domains, stdinDomains...)

    return domains
}

func readStdinLines() ([]string, error) {
    stat, _ := os.Stdin.Stat()
    if (stat.Mode() & os.ModeCharDevice) == 0 {
        var lines []string
        scanner := bufio.NewScanner(os.Stdin)
        for scanner.Scan() {
            line := strings.TrimSpace(scanner.Text())
            if line != "" {
                lines = append(lines, line)
            }
        }
        return lines, scanner.Err()
    }
    return nil, nil
}

func processDomain(ctx context.Context, domain string) {
    if !silent {
        log.Printf("[*] Processing %s", domain)
    }

    jsURLs := extractJSLinksFromHTML(domain)
    downloadJSFiles(jsURLs, threads, ctx)
}

func extractJSLinksFromHTML(baseUrl string) []string {
    resp, err := http.Get(baseUrl)
    if err != nil {
        if verbose {
            log.Printf("[-] Error fetching %s: %v", baseUrl, err)
        }
        return nil
    }
    defer resp.Body.Close()

    doc, err := goquery.NewDocumentFromReader(resp.Body)
    if err != nil {
        return nil
    }

    var urls []string
    doc.Find("script").Each(func(i int, s *goquery.Selection) {
        src, exists := s.Attr("src")
        if exists {
            urls = append(urls, src)
        }
    })

    matches := matchAndAdd(doc.Text())
    urls = append(urls, matches...)
    urls = unique(urls)
    return appendBaseUrl(urls, baseUrl)
}

func matchAndAdd(content string) []string {
    re := regexp.MustCompile(regexStr)
    return re.FindAllString(content, -1)
}

func downloadJSFiles(urls []string, concurrency int, ctx context.Context) {
    pipeline := gahttp.NewPipelineWithContext(ctx)
    pipeline.SetConcurrency(concurrency)
    for _, u := range urls {
        req := pipeline.Get(u, gahttp.Wrap(parseJSContent))
        req.Header.Set("User-Agent", "GoLinkFinder/1.1")
    }
    pipeline.Done()
    pipeline.Wait()
}

func parseJSContent(req *http.Request, resp *http.Response, err error) {
    if err != nil {
        if verbose {
            log.Println("[-]", err)
        }
        return
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        log.Fatal(err)
    }

    matches := matchAndAdd(string(body))
    for _, m := range matches {
        addResult(req.URL.String(), m)
    }
}

func addResult(url, value string) {
    mu.Lock()
    defer mu.Unlock()
    results = append(results, Result{URL: url, Value: value})
}

func printResults() {
    for _, r := range results {
        if filterStr != "" && !strings.Contains(r.Value, filterStr) {
            continue
        }
        switch format {
        case "json":
            jsonData, _ := json.Marshal(r)
            fmt.Println(string(jsonData))
        default:
            fmt.Println(r.Value)
        }
    }
}

func writeToFile() {
    if outputFile == "" {
        return
    }

    f, err := os.Create(outputFile)
    if err != nil {
        log.Fatalf("Could not create output file: %v", err)
    }
    defer f.Close()

    for _, r := range results {
        if filterStr != "" && !strings.Contains(r.Value, filterStr) {
            continue
        }
        switch format {
        case "json":
            jsonData, _ := json.Marshal(r)
            f.WriteString(string(jsonData) + "\n")
        default:
            f.WriteString(r.Value + "\n")
        }
    }
}
