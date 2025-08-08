package douyin

import (
	"fmt"
	"github.com/hr3lxphr6j/bililive-go/src/live"
	"github.com/hr3lxphr6j/bililive-go/src/live/internal"
	"github.com/hr3lxphr6j/bililive-go/src/pkg/utils"
	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	domain = "live.douyin.com"
	cnName = "抖音"

	randomCookieChars          = "1234567890abcdef"
	roomIdCatcherRegex         = `{\\"webrid\\":\\"([^"]+)\\"}`
	mainInfoLineCatcherRegex   = `self.__pace_f.push\(\[1,\s*"[^:]*:([^<]*,null,\{\\"state\\"[^<]*\])\\n"\]\)`
	commonInfoLineCatcherRegex = `self.__pace_f.push\(\[1,\s*\"(\{.*\})\"\]\)`
)

var roomInfoApiForSprintf = "https://live.douyin.com/webcast/room/web/enter/?aid=6383&app_name=douyin_web&live_id=1&device_platform=web&language=zh-CN&browser_language=zh-CN&browser_platform=Win32&browser_name=Chrome&browser_version=116.0.0.0&a_bogus=0&web_rid=%s"
var ttwid = ""

func init() {
	live.Register(domain, new(builder))
}

type builder struct{}

func (b *builder) Build(url *url.URL, opt ...live.Option) (live.Live, error) {
	return &Live{
		BaseLive:        internal.NewBaseLive(url, opt...),
		responseCookies: make(map[string]string),
	}, nil
}

func createRandomCookie() string {
	return utils.GenRandomString(21, randomCookieChars)
}

func createRandomOdintt() string {
	return utils.GenRandomString(160, randomCookieChars)
}

type Live struct {
	internal.BaseLive
	roomID                      string
	responseCookies             map[string]string
	LastAvailableStringUrlInfos []live.StreamUrlInfo
}

func (l *Live) parseRoomId() error {
	path := strings.Split(strings.ToLower(l.Url.Path), "/")
	if len(path) == 0 {
		return fmt.Errorf("failed to parse room id from url: %s", l.Url.String())
	}
	roomID := path[len(path)-1]
	if roomID[0] == '+' {
		roomID = roomID[1:]
	}
	l.roomID = roomID
	return nil
}

func (l *Live) getTtwid() (string, error) {
	if ttwid != "" {
		return ttwid, nil
	}
	cookies := l.Options.Cookies.Cookies(l.Url)
	cookieKVs := make(map[string]string)
	for _, item := range cookies {
		cookieKVs[item.Name] = item.Value
	}
	for key, value := range l.responseCookies {
		cookieKVs[key] = value
	}
	resp, err := requests.Get(
		"https://live.douyin.com/1-2-3-4-5-6-7-8-9-0",
		live.CommonUserAgent,
		requests.Cookies(cookieKVs),
	)
	if err != nil {
		return "", err
	}
	switch code := resp.StatusCode; code {
	case http.StatusOK:
		for _, cookie := range resp.Cookies() {
			if cookie.Name == "ttwid" {
				ttwid = cookie.Value
				return ttwid, nil
			}
		}
	default:
		return "", fmt.Errorf("failed to get page, code: %v, %w", code, live.ErrInternalError)
	}
	return "", fmt.Errorf("failed to get ttwid")
}

func (l *Live) GetData() (info *live.Info, streamUrlInfos []live.StreamUrlInfo, err error) {
	if l.roomID == "" {
		err = l.parseRoomId()
		if err != nil {
			return
		}
	}
	if ttwid, ok := l.responseCookies["ttwid"]; !ok {
		ttwid, err = l.getTtwid()
		if err != nil {
			return
		}
		l.responseCookies["ttwid"] = ttwid
		time.Sleep(2 * time.Second)
	}
	cookieKVs := make(map[string]string)
	cookies := l.Options.Cookies.Cookies(l.Url)
	for _, item := range cookies {
		cookieKVs[item.Name] = item.Value
	}
	for key, value := range l.responseCookies {
		cookieKVs[key] = value
	}
	roomInfoApi := fmt.Sprintf(roomInfoApiForSprintf, l.roomID)
	resp, err := requests.Get(
		roomInfoApi,
		live.CommonUserAgent,
		requests.Cookies(cookieKVs),
	)
	if err != nil {
		return
	}
	switch code := resp.StatusCode; code {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, nil, live.ErrRoomNotExist
	default:
		return nil, nil, fmt.Errorf("failed to get page, code: %v, %w", code, live.ErrInternalError)
	}
	body, err := resp.Text()
	if err != nil {
		return
	}
	result := gjson.Parse(body)
	if !result.Get("data").Exists() {
		return nil, nil, fmt.Errorf("get room info failed")
	}
	info = &live.Info{
		Live:     l,
		HostName: result.Get("data.user.nickname").String(),
		RoomName: result.Get("data.data.0.title").String(),
		Status:   result.Get("data.data.0.status").Int() == 2,
	}
	if !info.Status {
		return info, nil, nil
	}
	if !result.Get("data.data.0.stream_url.live_core_sdk_data.pull_data.stream_data").Exists() {
		return nil, nil, fmt.Errorf("stream data not found")
	}
	streamData := gjson.Parse(result.Get("data.data.0.stream_url.live_core_sdk_data.pull_data.stream_data").String())
	if !streamData.Exists() || !streamData.Get("data").Exists() {
		return nil, nil, fmt.Errorf("stream data not found")
	}
	streamData.Get("data").ForEach(func(key, value gjson.Result) bool {
		flv := value.Get("main.flv").String()
		var Url *url.URL
		Url, err = url.Parse(flv)
		if err != nil {
			return true
		}
		paramsString := value.Get("main.sdk_params").String()
		paramsJson := gjson.Parse(paramsString)
		var description strings.Builder
		paramsJson.ForEach(func(key, value gjson.Result) bool {
			description.WriteString(key.String())
			description.WriteString(": ")
			description.WriteString(value.String())
			description.WriteString("\n")
			return true
		})
		Resolution := 0
		resolution := strings.Split(paramsJson.Get("resolution").String(), "x")
		if len(resolution) == 2 {
			x, err := strconv.Atoi(resolution[0])
			if err != nil {
				return true
			}
			y, err := strconv.Atoi(resolution[1])
			if err != nil {
				return true
			}
			Resolution = x * y
		}
		Vbitrate := int(paramsJson.Get("vbitrate").Int())
		streamUrlInfos = append(streamUrlInfos, live.StreamUrlInfo{
			Name:        key.String(),
			Description: description.String(),
			Url:         Url,
			Resolution:  Resolution,
			Vbitrate:    Vbitrate,
		})
		return true
	})

	// 按清晰度优先级排序, 原画origin 蓝光uhd 超清hd 高清sd 标清ld 流畅md 仅音频ao
	keyOrder := []string{"origin", "uhd", "hd", "sd", "ld", "md"}
	sort.Slice(streamUrlInfos, func(i, j int) bool {
		var index1, index2 = -1, -1
		for index, name := range keyOrder {
			if streamUrlInfos[i].Name == name {
				index1 = index
			} else if streamUrlInfos[j].Name == name {
				index2 = index
			}
			if index1 != -1 && index2 != -1 {
				break
			}
		}
		if index1 == -1 {
			return false
		}
		if index2 == -1 {
			return true
		}
		return index1 < index2
	})
	return info, streamUrlInfos, nil
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	var streamUrlInfos []live.StreamUrlInfo
	info, streamUrlInfos, err = l.GetData()
	if err != nil {
		return
	}
	l.LastAvailableStringUrlInfos = streamUrlInfos
	return
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	if l.LastAvailableStringUrlInfos != nil {
		us = make([]*url.URL, 0, len(l.LastAvailableStringUrlInfos))
		for _, urlInfo := range l.LastAvailableStringUrlInfos {
			us = append(us, urlInfo.Url)
		}
		return
	}
	return nil, fmt.Errorf("failed douyin GetStreamUrls()")
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
