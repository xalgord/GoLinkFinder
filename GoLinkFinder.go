/*
 https://0xsha.io
 by @0xsha 1/2020
 Modified: Support for stdin and targets list
*/

package main

import (
    "bufio"
    "fmt"
    "io"
    "log"
    "net/http"
    "os"
    "regexp"
    "strings"

    // repos makes things easier
    "github.com/PuerkitoBio/goquery"
    "github.com/akamensky/argparse"
    "github.com/tomnomnom/gahttp"
)

/// Global variables ///

const version = "1.0.0-alpha"

const concurrency = 10

// regex foo from https://github.com/GerbenJavado/LinkFinder
const regexStr = `(?:"|')(((?:[a-zA-Z]{1,10}://|//)[^"'/]{1,}\.[a-zA-Z]{2,}[^"']{0,})|((?:/|\.\./|\./)[^"'><,;| *()(%%$^/\\\[\]][^"'><,;|()]{1,})|([a-zA-Z0-9_\-/]{1,}/[a-zA-Z0-9_\-/]{1,}\.(?:[a-zA-Z]{1,4}|action)(?:[\?|#][^"|']{0,}|))|([a-zA-Z0-9_\-/]{1,}/[a-zA-Z0-9_\-/]{3,}(?:[\?|#][^"|']{0,}|))|([a-zA-Z0-9_\-]{1,}\.(?:php|asp|aspx|jsp|json|action|html|js|txt|xml)(?:[\?|#][^"|']{0,}|)))(?:"|')`

var founds []string

func unique(strSlice []string) []string {
    keys := make(map[string]bool)
    list := []string{}
    for _, entry := range strSlice {
        if _, value := keys[entry]; !value {
            keys[entry] = true
            list = append(list, entry)
        }
    }
    return list
}

func downloadJSFile(urls []string, concurrency int) {
    pipeLine := gahttp.NewPipeline()
    pipeLine.SetConcurrency(concurrency)
    for _, u := range urls {
        pipeLine.Get(u, gahttp.Wrap(parseFile, gahttp.CloseBody))
    }
    pipeLine.Done()
    pipeLine.Wait()
}

func parseFile(req *http.Request, resp *http.Response, err error) {
    if err != nil {
        return
    }
    body, err := io.ReadAll(resp.Body)
    if err != nil {
        log.Fatal(err)
    }
    matchAndAdd(string(body))
}

func extractUrlFromJS(urls []string, baseUrl string) []string {
    urls = unique(urls)
    var cleaned []string
    for i := 1; i < len(urls); i++ {
        urls[i] = strings.ReplaceAll(urls[i], "'", "")
        urls[i] = strings.ReplaceAll(urls[i], "\"", "")
        if len(urls[i]) < 5 {
            continue
        }
        if !strings.Contains(urls[i], ".js") {
            continue
        }
        if urls[i][:4] == "http" || urls[i][:5] == "https" {
            cleaned = append(cleaned, urls[i])
        } else if urls[i][:2] == "//" {
            cleaned = append(cleaned, "https:"+urls[i])
        } else if urls[i][:1] == "/" {
            cleaned = append(cleaned, baseUrl+urls[i])
        }
    }
    return cleaned
}

func matchAndAdd(content string) []string {
    regExp, err := regexp.Compile(regexStr)
    if err != nil {
        log.Fatal(err)
    }
    links := regExp.FindAllString(content, -1)
    for _, link := range links {
        founds = append(founds, link)
    }
    return founds
}

func appendBaseUrl(urls []string, baseUrl string) []string {
    urls = unique(urls)
    var n []string
    for _, url := range urls {
        n = append(n, baseUrl+strings.TrimSpace(url))
    }
    return n
}

func extractJSLinksFromHTML(baseUrl string) []string {
    resp, err := http.Get(baseUrl)
    if err != nil {
        log.Println("Error fetching URL:", baseUrl, "-", err)
        return nil
    }
    defer resp.Body.Close()

    if resp.Body == nil {
        log.Println("Response body is nil for", baseUrl)
        return nil
    }

    goos, err := goquery.NewDocumentFromReader(resp.Body)
    if err != nil {
        log.Println("Error parsing HTML for", baseUrl, "-", err)
        return nil
    }

    var htmlJS = matchAndAdd(goos.Find("script").Text())
    var urls = extractUrlFromJS(htmlJS, baseUrl)

    goos.Find("script").Each(func(i int, s *goquery.Selection) {
        src, _ := s.Attr("src")
        urls = append(urls, src)
    })

    urls = appendBaseUrl(urls, baseUrl)
    return urls
}

func prepareResult(result []string) []string {
    for i := 0; i < len(result); i++ {
        result[i] = strings.ReplaceAll(result[i], "\"", "")
        result[i] = strings.ReplaceAll(result[i], "'", "")
    }
    return result
}

func readLines(path string) ([]string, error) {
    file, err := os.Open(path)
    if err != nil {
        return nil, err
    }
    defer file.Close()

    var lines []string
    scanner := bufio.NewScanner(file)

    for scanner.Scan() {
        lines = append(lines, scanner.Text())
    }
    return lines, scanner.Err()
}

func readStdinLines() ([]string, error) {
    stat, _ := os.Stdin.Stat()
    if (stat.Mode() & os.ModeCharDevice) == 0 {
        // Input is being piped
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

func processDomain(domain string) {
    var baseUrl = domain
    if !strings.HasPrefix(baseUrl, "http://") && !strings.HasPrefix(baseUrl, "https://") {
        baseUrl = "https://" + baseUrl
    }

    log.Printf("[*] Processing %s\n", baseUrl)

    var htmlUrls = extractJSLinksFromHTML(baseUrl)
    downloadJSFile(htmlUrls, concurrency)
}

func main() {

    parser := argparse.NewParser("goLinkFinder", "GoLinkFinder")
    domain := parser.String("d", "domain", &argparse.Options{Required: false, Help: "Input a URL."})
    output := parser.String("o", "out", &argparse.Options{Required: false, Help: "Output file name (e.g: output.txt)"})
    listFile := parser.String("l", "list", &argparse.Options{Required: false, Help: "Path to a file containing newline-separated URLs."})

    err := parser.Parse(os.Args)
    if err != nil {
        fmt.Print(parser.Usage(err))
        return
    }

    var domains []string

    // Single domain
    if *domain != "" {
        domains = append(domains, *domain)
    }

    // List from file
    if *listFile != "" {
        list, err := readLines(*listFile)
        if err != nil {
            log.Fatalf("Failed to read list file: %v", err)
        }
        domains = append(domains, list...)
    }

    // Read from stdin if available
    stdinDomains, err := readStdinLines()
    if err != nil {
        log.Fatalf("Error reading from stdin: %v", err)
    }
    if len(stdinDomains) > 0 {
        domains = append(domains, stdinDomains...)
    }

    // If no domains provided at all
    if len(domains) == 0 {
        log.Fatal("No domains provided. Use -d, -l, or pipe input via stdin.")
    }

    // Process each domain
    for _, d := range domains {
        processDomain(d)
    }

    founds = unique(founds)
    founds = prepareResult(founds)

    for _, found := range founds {
        fmt.Println(found)
    }

    if *output != "" {
        f, err := os.OpenFile("./"+*output, os.O_CREATE|os.O_WRONLY, 0644)
        if err != nil {
            log.Println(err)
        }
        defer f.Close()

        for _, found := range founds {
            if _, err := f.WriteString(found + "\n"); err != nil {
                log.Fatal(err)
            }
        }
    }
}
