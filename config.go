package autodelete

import (
	"fmt"
	"io/ioutil"
	"os"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"gopkg.in/yaml.v2"
)

type Bot struct {
	Config
	s  *discordgo.Session
	me *discordgo.User

	mu       sync.RWMutex
	channels map[string]*ManagedChannel

	reaper *reapQueue
}

func New(c Config) *Bot {
	b := &Bot{
		Config:   c,
		channels: make(map[string]*ManagedChannel),
		reaper:   newReapQueue(),
	}
	go b.reapWorker()
	return b
}

type Config struct {
	ClientID     string `yaml:"clientid"`
	ClientSecret string `yaml:"clientsecret"`
	BotToken     string `yaml:"bottoken"`
	HTTP         struct {
		Listen string `yaml:"listen"`
		Public string `yaml:"public"`
	} `yaml:"http"`
	//Database struct {
	//	Driver string `yaml:"driver"`
	//	URL    string `yaml:"url"`
	//} `yaml:"db,flow"`
}

type managedChannelMarshal struct {
	ID            string        `yaml:"id"`
	ConfMessageID string        `yaml:"conf_message_id"`
	LiveTime      time.Duration `yaml:"live_time"`
	MaxMessages   int           `yaml:"max_messages"`
}

const pathChannelConfig = "./data/%s.yml"

func (b *Bot) SaveAllChannelConfigs() []error {
	var wg sync.WaitGroup
	errCh := make(chan error)

	b.mu.RLock()
	for channelID := range b.channels {
		wg.Add(1)
		go func() {
			errCh <- b.SaveChannelConfig(channelID)
			wg.Done()
		}()
	}
	b.mu.RUnlock()

	go func() {
		wg.Wait()
		close(errCh)
	}()

	var errs []error
	for v := range errCh {
		if v != nil {
			errs = append(errs, v)
		}
	}
	return errs
}

func (b *Bot) SaveChannelConfig(channelID string) error {
	b.mu.RLock()
	manCh := b.channels[channelID]
	b.mu.RUnlock()
	if manCh == nil {
		return nil
	}

	by, err := yaml.Marshal(manCh.Export())
	if err != nil {
		panic(err)
	}
	fileName := fmt.Sprintf(pathChannelConfig, channelID)
	f, err := os.Create(fileName)
	if err != nil {
		return err
	}
	f.Write(by)
	err = f.Close()
	if err != nil {
		return err
	}
	return nil
}

func (b *Bot) LoadChannelConfigs() []error {
	var wg sync.WaitGroup
	var errCh = make(chan error)

	b.s.State.RLock()
	for _, v := range b.s.State.Guilds {
		fmt.Println(v)
		for _, w := range v.Channels {
			wg.Add(1)
			go func() {
				errCh <- b.loadChannel(w.ID)
				wg.Done()
			}()
		}
	}
	b.s.State.RUnlock()

	go func() {
		wg.Wait()
		close(errCh)
	}()

	var errs []error
	for v := range errCh {
		errs = append(errs, v)
	}
	return errs
}

func (b *Bot) loadChannel(channelID string) error {
	fileName := fmt.Sprintf(pathChannelConfig, channelID)
	f, err := os.Open(fileName)
	if os.IsNotExist(err) {
		fmt.Println("no config for", channelID)
		b.mu.Lock()
		b.channels[channelID] = nil
		b.mu.Unlock()
		return nil
	} else if err != nil {
		return err
	}
	by, err := ioutil.ReadAll(f)
	f.Close()
	if err != nil {
		return err
	}
	var conf managedChannelMarshal
	err = yaml.Unmarshal(by, &conf)
	if err != nil {
		return err
	}

	conf.ID = channelID
	fmt.Println("loading channel", channelID)
	mCh, err := InitChannel(b, conf)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.channels[channelID] = mCh
	b.mu.Unlock()

	err = mCh.LoadBacklog()
	if err != nil {
		fmt.Println(err)
		return err
	}
	return nil
}
