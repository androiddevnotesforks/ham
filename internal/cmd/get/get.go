package get

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/mkideal/cli"

	"github.com/charmbracelet/lipgloss"

	"github.com/antony-jr/ham/internal/banner"
	"github.com/antony-jr/ham/internal/core"
	"github.com/antony-jr/ham/internal/helpers"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

const (
	HAM_LINUX_BINARY_URL string = "https://github.com/antony-jr/ham/releases/download/stable/ham-build-linux-amd64"
)

type getT struct {
	cli.Helper

	NoConfirm               bool   `cli:"n,no-confirm" usage:"Auto Confirm 'Yes' to all questions for the user. (Use with Caution)"`
	Answers                 string `cli:"a,answers" usage:"Path to answers.json to Answer required Questions."`
	KeepServer              bool   `cli:"k,keep-server" usage:"Don't Destroy the Remote Server on any error."`
	KeepServerOnConnectFail bool   `cli:"s,keep-server-conn-fail" usage:"Don't Destroy the Remote Server even if we can't SSH into it."`
	KeepServerOnTrackFail   bool   `cli:"t,keep-server-track-fail" usage:"Don't Destroy the Remote Server even if Tracking Fails."`
	KeepServerOnBuildFail   bool   `cli:"b,keep-server-build-fail" usage:"Don't Destroy the Remote Server even if Build Fails. (Use with Caution)"`
	TestingBinary           string `cli:"e,testing-binary" usage:"Path to ham-build binary to use in the Remote Server during Testing. (Developer)"`
	TestingSSHIP            string `cli:"i,testing-ssh-ip" usage:"Run a Test Run without Creating Servers and Use the given IP as Build Server. (Developer)"`
	Force                   bool   `cli:"f,force" usage:"Force start a build even if the recipe was built Already."`
}

func ParseGitRemoteString(remote string) (string, string) {
	urlSlice := strings.Split(remote, "://")

	if len(urlSlice) == 2 {
		remote = urlSlice[1]
	}

	slice := strings.Split(remote, "/")

	branchSlice := strings.Split(remote, ":")
	branch := ""
	url := ""

	if len(branchSlice) == 2 {
		url = branchSlice[0]
		branch = branchSlice[1]
	} else {
		url = remote
	}

	if len(urlSlice) == 2 {
		url = fmt.Sprintf("%s://%s", urlSlice[0], url)
	}

	if len(slice) != 2 {
		return url, branch
	}

	user := slice[0]
	userSlice := strings.Split(user, "@")

	if len(userSlice) != 2 {
		return url, branch
	}

	uname := userSlice[0]
	host := userSlice[1]

	if strings.ToLower(host) != "gh" {
		return url, branch
	}

	repo := slice[1]
	repoSlice := strings.Split(repo, ":")

	if len(repoSlice) == 2 {
		repo = repoSlice[0]
	}

	// Official HAM Recipes.
	if uname == "~" {
		return fmt.Sprintf("https://github.com/ham-community/%s", repo), branch
	}

	return fmt.Sprintf("https://github.com/%s/%s", uname, repo), branch
}

func NewCommand() *cli.Command {
	return &cli.Command{
		Name: "get",
		Desc: "Get a build of AOSP from community recipe or locally using your Hetzner Cloud",
		Text: `
Syntax: ham get [RECIPE LOCATION]

Recipe from Ham Community:
   ham get ~@gh/enchilada_los18.1
   ham get ~@gh/enchilada_los18.1:bleeding

Recipe from Github:
   ham get user@gh/repo:branch
   ham get antony-jr@gh/enchilada_los18.1
   ham get antony-jr@gh/ecnhilada_los18.1:dev

Recipe from Git:
   ham get https://antonyjr.in/enchilada_los181.git

Local Recipe:
   ham get ./examples/enchilada_los18.1`,
		Argv: func() interface{} { return new(getT) },
		NumArg: func(n int) bool {
			if n != 1 {
				return false
			}
			return true
		},
		Fn: func(ctx *cli.Context) error {
			argv := ctx.Argv().(*getT)
			args := ctx.Args()
			if len(args) != 1 {
				return nil
			}
			recipe_src := args[0]
			dir := recipe_src
			testingRun := len(argv.TestingSSHIP) != 0
			tuiSpinnerMsg := NewTUISpinnerMessenger()
			defer tuiSpinnerMsg.StopMessage()

			peacefulQuit := true

			if testingRun {
				if runtime.GOOS != "linux" {
					return errors.New("OS Not Supported for Testing. Get a Linux Machine to Develop HAM.")
				}

				if len(argv.TestingBinary) == 0 {
					return errors.New("Testing Binary Path Not Given, Please give a Testing Binary Path.")
				}
			}

			//checkMark := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).SetString("✓")
			//optionalSuffix := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).SetString(" (OPTIONAL, Press ENTER to Skip)")

			banner.GetStartBanner()

			tuiSpinnerMsg.ShowMessage(fmt.Sprintf("Parsing %s...", recipe_src))

			//fmt.Printf(" %s Parsing %s...\n", checkMark, recipe_src)
			remove := false
			usedGit := false
			gitUrl := ""
			gitBranch := ""
			if _, err := os.Stat(recipe_src); os.IsNotExist(err) {
				// Recipe is not local, so use git to clone the
				// the recipe requested by the user.
				// banner.GetRecipeNotExistsBanner()
				tuiSpinnerMsg.ShowMessage("Recipe Does not Exists, Cloning.. ")

				// Parse the string
				git_url, git_branch := ParseGitRemoteString(recipe_src)

				gitUrl = git_url
				gitBranch = git_branch

				if git_branch == "" {
					git_branch = "Default"
				}

				_ = tuiSpinnerMsg.StopMessage()

				fmt.Printf(" %s Git URL: %s\n", checkMark, git_url)
				fmt.Printf(" %s Git Branch: %s\n", checkMark, git_branch)

				uniqueTempDir, err := os.MkdirTemp(os.TempDir(), "*-ham-recipe")
				if err != nil {
					return err
				}
				dir = uniqueTempDir
				remove = true
				usedGit = true

				tuiSpinnerMsg.ShowMessage(fmt.Sprintf("Cloning Into %s...", dir))

				if gitBranch == "" {
					_, err = git.PlainClone(dir, false, &git.CloneOptions{
						URL: gitUrl,
					})
				} else {
					_, err = git.PlainClone(dir, false, &git.CloneOptions{
						URL:           gitUrl,
						ReferenceName: plumbing.NewBranchReferenceName(gitBranch),
					})
				}

				if err != nil {
					_ = os.RemoveAll(dir)
					return err
				}

				_ = tuiSpinnerMsg.StopMessage()
			}

			if remove {
				defer os.RemoveAll(dir)
			}

			if testingRun {
				fmt.Printf(" ! RUNNING IN TESTING MODE ! \n")
			}

			// Parse recipe file for meta information
			// and args information.
			hf, err := core.NewHAMFile(dir)
			if err != nil {
				return err
			}
			serverName := helpers.ServerNameFromSHA256(hf.SHA256Sum)

			banner.GetRecipeBanner(hf.Title, hf.Version, hf.SHA256Sum)

			tuiSpinnerMsg.ShowMessage("Reading Configuration...")
			config, err := core.GetConfiguration()
			if err != nil {
				return err
			}
			_ = tuiSpinnerMsg.StopMessage()
			fmt.Printf(" %s Read Configuration\n", checkMark)

			tuiSpinnerMsg.ShowMessage("Connecting to Hetzner...")
			client := hcloud.NewClient(hcloud.WithToken(config.APIKey))
			_ = tuiSpinnerMsg.StopMessage()
			fmt.Printf(" %s Connected with Hetzner Cloud API\n", checkMark)

			tuiSpinnerMsg.ShowMessage("Checking SSH Keys... ")
			sshkeys, err := client.SSHKey.All(
				context.Background(),
			)
			if err != nil {
				return err
			}

			// Search for ham-ssh-key SSH Key,
			// if it does not exists then error out
			// asking the user to properly init.
			keyOk := false
			keyFingerprint, err := helpers.GetSSHFingerprint(config.SSHPublicKey)

			if err != nil {
				return err
			}

			var ham_labels map[string]string

			for _, el := range sshkeys {
				if el.Name == "ham-ssh-key" {
					if keyFingerprint == el.Fingerprint {
						keyOk = true
						ham_labels = el.Labels
					}
					break
				}
			}

			if !keyOk {
				return errors.New("HAM SSH Key not found, Please Re-Initialize.")
			}

			peacefulQuit = tuiSpinnerMsg.StopMessage()
			if !peacefulQuit {
				return nil
			}

			fmt.Printf(" %s Verified SSH Keys\n", checkMark)

			tuiSpinnerMsg.ShowMessage("Destroying Dead Servers...")
			// Destroy all dead servers
			// whenver we see them.
			// This is highly unlikely that our ham leaves dead servers
			// but this is just a precaution.
			err = helpers.DestroyAllDeadServers(client)
			if err != nil {
				return err
			}

			tuiSpinnerMsg.ShowMessage("Searching for Active Builds...")
			// Search for build servers that were already started
			// if found, track that.
			servers, err := client.Server.All(
				context.Background(),
			)
			if err != nil {
				return err
			}

			var currentBuildServer *hcloud.Server
			serverRunning := false
			for _, server := range servers {
				if server.Name == serverName {
					_ = tuiSpinnerMsg.StopMessage()
					// Track status instead of creating a new one.
					currentBuildServer = server
					fmt.Printf(" %s Active Build Found\n", checkMark)
					serverRunning = true
					break
				}
			}

			previousBuildStatus := ""
			tuiSpinnerMsg.ShowMessage("Checking Previous Builds...")
			for key, status := range ham_labels {
				if key == serverName && !argv.Force {
					previousBuildStatus = status
					break
				}
			}
			_ = tuiSpinnerMsg.StopMessage()

			fmt.Printf(" %s Checked Previous Builds\n", checkMark)

			if previousBuildStatus != "" && !argv.Force {
				if previousBuildStatus == "failed" ||
					previousBuildStatus == "successful" {
					estr := fmt.Sprintf("A %s build had run before with this recipe, Run with -f flag to force build.",
						previousBuildStatus)
					return errors.New(estr)

				}
			}

			// This is a safety net.
			destroyServer := !argv.KeepServer
			defer deferDeleteServer(client, &destroyServer, serverName)

			// Hmm... My ISP and mostly a lot of dumb ISP's don't support IPv6
			// and tunnel is a waste of time. Also IPv4 cost a little extra on
			// hetzner. We have no choice but to use IPv4 to support all kinds
			// of client devices even android phones.
			// var ip6Addr string
			var ipAddr string
			if testingRun {
				ipAddr = argv.TestingSSHIP
			} else {
				if currentBuildServer != nil {
					ipAddr = fmt.Sprintf("%s:22", currentBuildServer.PublicNet.IPv4.IP.String())
				} else {
					ipAddr = "127.1:22"
				}
			}

			if !serverRunning && !testingRun {
				// Create a new build server.

				// Before that we need to get variables from the user
				// such as special files, env vars required for the
				// build from the user. This might be crucial secrets
				// so transport it with SSH to stay secure.
				varsFilePath, fileUploads, err := askQuestions(&hf, serverName, argv.Answers, argv.NoConfirm)
				if err != nil {
					return err
				}
				defer os.Remove(varsFilePath)

				tuiSpinnerMsg.ShowMessage("Getting Server Information... ")

				// Get Suitable Server and Price
				price, serverType, err := GrossServerPriceForServerWithHighestPerformance(client)
				if err != nil {
					return err
				}
				_ = tuiSpinnerMsg.StopMessage()
				banner.GetServerPriceInformationBanner(strings.ToUpper(serverType.Name), price)

				confirmCreate := argv.NoConfirm

				if !argv.NoConfirm {
					err = runConfirmCreateTeaProgram(&confirmCreate)
					if err != nil {
						return err
					}
				}

				// Keep this condition simple since this is an important
				// decision by the user.
				if confirmCreate == false {
					return errors.New("User Declined to Create a New Server.")
				} else {
					/* NOTE: Important Section. */
					tuiSpinnerMsg.ShowMessage("Creating Server... ")
					server, err := core.CreateServer(client, serverType, serverName)
					if err != nil {
						destroyServer = !argv.KeepServer
						return err
					}
					currentBuildServer = server
					ipAddr = fmt.Sprintf("%s:22", currentBuildServer.PublicNet.IPv4.IP.String())
					_ = tuiSpinnerMsg.StopMessage()
					fmt.Printf(" %s Created Server\n", checkMark)
				}

				volDevice, err := helpers.GetVolumeLinuxDeviceForServer(client, serverName)
				if err != nil {
					return err
				}
				fmt.Printf(" %s Volume Device: %s\n", checkMark, volDevice)

				err = doInitialize(ipAddr, config.SSHPrivateKey, volDevice, varsFilePath, fileUploads, usedGit, gitUrl, gitBranch, dir, argv.TestingBinary)
				if err != nil {
					return err
				}
			}

			_ = tuiSpinnerMsg.StopMessage()

			// Check if build is running on the remote server
			// if not then start it now.

			{
				tuiSpinnerMsg.ShowMessage("Checking Build Process... ")
				sshClient, err := GetSSHClient(ipAddr, config.SSHPrivateKey)
				tries := 0
				for {
					tries++
					if err != nil {
						if tries > 20 {
							return err
						}
						time.Sleep(time.Second * time.Duration(2))
						sshClient, err = GetSSHClient(ipAddr, config.SSHPrivateKey)
						continue
					}
					break
				}
				tries = 0
				defer sshClient.Close()

				shell, err := GetSSHShell(sshClient)
				for {
					tries++
					if err != nil {
						if tries > 20 {
							return err
						}
						time.Sleep(time.Second * time.Duration(2))
						shell, err = GetSSHShell(sshClient)
						continue
					}
					break
				}
				tries = 0

				tryExec := func(cmd string) (string, error) {
					out, err := shell.Exec(cmd)
					try := 0
					for {
						try++
						if err != nil {
							if try > 20 {
								return "", err
							}
							time.Sleep(time.Second * time.Duration(2))
							out, err = shell.Exec(cmd)
							continue
						}
						break
					}
					return out, err
				}

				processExists, _ := shell.Exec("ps -ef | grep \"[h]am build\"")
				if err != nil {
					return err
				}

				if strings.Contains(processExists, "ham build") {
					_ = tuiSpinnerMsg.StopMessage()
					fmt.Printf(" %s Build Process Running\n", checkMark)
				} else {
					keep := ""
					if argv.KeepServer || argv.KeepServerOnBuildFail {
						keep = "--keep-server"
					}
					buildCommand := fmt.Sprintf("ham build %s --sum %s --recipe /ham-recipe --vars /ham-files/vars.json",
						keep,
						hf.SHA256Sum)
					// check if initialized first
					out, _ := shell.Exec("ls /tmp/ | grep ham.init.finished")
					if !strings.Contains(out, "ham.init.finished") {
						_ = tuiSpinnerMsg.StopMessage()
						fmt.Println(" Server is not Initialized Properly")
						fmt.Println(" Please Answer All Questions to Initialize Properly")
						varsFilePath, fileUploads, err := askQuestions(&hf, serverName, argv.Answers, argv.NoConfirm)
						tries = 0
						for {
							tries++
							if err != nil {
								if tries > 4 {
									return err
								}
								time.Sleep(time.Second * time.Duration(1))
								varsFilePath, fileUploads, err = askQuestions(&hf, serverName, argv.Answers, argv.NoConfirm)
								continue
							}
							break
						}
						tries = 0
						defer os.Remove(varsFilePath)

						volDevice, err := helpers.GetVolumeLinuxDeviceForServer(client, serverName)
						if err != nil {
							return err
						}
						fmt.Printf(" %s Volume Device: %s\n", checkMark, volDevice)

						err = doInitialize(ipAddr, config.SSHPrivateKey, volDevice, varsFilePath, fileUploads, usedGit, gitUrl, gitBranch, dir, argv.TestingBinary)
						if err != nil {
							return err
						}

						time.Sleep(time.Second * time.Duration(2))
					}

					// Cleanup any previous builds
					_, err = tryExec("rm -rf /tmp/*.ham.command.status")
					_, err = tryExec("rm -rf /tmp/*.ham.stdout")
					if err != nil {
						return err
					}

					_, err = tryExec(buildCommand)
					if err != nil {
						return err
					}
					_ = tuiSpinnerMsg.StopMessage()
					time.Sleep(time.Second * time.Duration(2))
				}
			}

			banner.GetCmdProgressBanner()

			tries := 0
			for {
				outputChannel := TailRemoteStdout(ipAddr, config.SSHPrivateKey, hf.SHA256Sum)
				sshCode, err := trackRemoteServerProgress(ipAddr, config.SSHPrivateKey, outputChannel)

				// Check for SSH Shell Code for More
				// accurate errors.
				if sshCode != SSH_SHELL_NO_ERROR {
					if sshCode == SSH_SHELL_CANNOT_GET_CLIENT ||
						sshCode == SSH_SHELL_CANNOT_GET_SESSION ||
						sshCode == SSH_SHELL_CANNOT_CONNECT {
						tries++
						if tries >= 3 {
							if argv.KeepServer || argv.KeepServerOnConnectFail {
								destroyServer = false
								banner.GetConnectFailBanner(serverName)
								return errors.New(
									"Cannot Get SSH Client (" + err.Error() + "), But Server is Kept and Still Running.")
							}

							delErr := helpers.TryDeleteServer(client, serverName, 20, 5)
							if delErr != nil {
								banner.GetConnectFailBanner(serverName)
								return delErr
							}

							destroyServer = false
							return errors.New("Cannnot Get SSH Client (" + err.Error() + "). Destroyed Server.")
						}

						time.Sleep(time.Second * time.Duration(5))
						continue
					} else if sshCode == SSH_SHELL_MALFORMED_JSON {
						tries++
						if tries < 20 {
							time.Sleep(time.Second * time.Duration(10))
							continue
						}

						if argv.KeepServer || argv.KeepServerOnTrackFail {
							destroyServer = false
							banner.GetMalformedJSONBanner(serverName)
							return errors.New("Malformed JSON from Build Server, But Server is Kept and Still Running.")
						}

						delErr := helpers.TryDeleteServer(client, serverName, 20, 5)
						if delErr != nil {
							banner.GetMalformedJSONBanner(serverName)
							return delErr
						}

						destroyServer = false
						return errors.New("Malformed JSON from Build Server. Destroyed Server")
					} else if sshCode == SSH_SHELL_HAM_STATUS_ERRORED {
						if argv.KeepServer || argv.KeepServerOnBuildFail {
							destroyServer = false
							banner.GetBuildFailedBanner(serverName)
							return errors.New("Remote Build Failed, But Server is Kept and Still Running.")
						}

						delErr := helpers.TryDeleteServer(client, serverName, 20, 5)
						if delErr != nil {
							banner.GetBuildFailedBanner(serverName)
							return delErr
						}

						destroyServer = false
						return errors.New("Remote Build Failed. Destroyed Server.")
					} else {
						tries++
						if tries >= 3 {
							delErr := helpers.TryDeleteServer(client, serverName, 20, 5)
							if delErr != nil {
								return delErr
							}

							destroyServer = false
							return errors.New("Unknown Build Error. Destroyed Server.")
						}
					}
				} else {
					if err != nil {
						return err
					}

					destroyServer = false
					break
				}

			}

			tuiSpinnerMsg.ShowMessage("Fetching Build Status... ")
			statusTries := 0
			for statusTries < 20 {
				statusTries++

				targetSSHKey, _, err := client.SSHKey.Get(
					context.Background(),
					"ham-ssh-key",
				)
				if err != nil {
					time.Sleep(time.Second * time.Duration(10))
					continue
				}

				if targetSSHKey == nil {
					destroyServer = !argv.KeepServer
					return errors.New("HAM SSH Key not found at Hetzner Project.")
				}

				labels := targetSSHKey.Labels
				for serv, buildStatus := range labels {
					if serv == serverName {
						_ = tuiSpinnerMsg.StopMessage()
						if buildStatus == "successful" {
							destroyServer = !argv.KeepServer
							fmt.Println("Build Successful")
						} else if buildStatus == "inprogress" {
							fmt.Println("Build in Progress")
						} else {
							destroyServer = !argv.KeepServer || !argv.KeepServerOnBuildFail
						}
						return nil
					}
				}

				destroyServer = !argv.KeepServer
				_ = tuiSpinnerMsg.StopMessage()
				break
			}

			banner.GetMalformedJSONBanner(serverName)
			return errors.New("Cannot Get Status of Build.")
		},
	}
}

// This is defer delete, might come in handy when user exits the
// program with Ctrl+Z or some other means, as long it's not killed
// it can try to delete any created server. The state is checked if
// we have to delete the server since it may not be desired by the
// user.
func deferDeleteServer(client *hcloud.Client, destroy *bool, serverName string) {
	if destroy != nil && *destroy {
		helpers.TryDeleteServer(client, serverName, 5, 5)
	}
}

func doInitialize(ipAddr string,
	privateKey string,
	volumeLinuxDevice string,
	varsFilePath string,
	fileUploads map[string]string,
	usedGit bool,
	gitUrl string,
	gitBranch string,
	dir string,
	testingBin string) error {
	spinnerMsg := NewTUISpinnerMessenger()
	defer spinnerMsg.StopMessage()

	// Install HAM Linux Binary to the Server
	spinnerMsg.ShowMessage("Installing HAM to Remote Server... ")

	sshTries := 0
	sshShellClient, err := GetSSHClient(ipAddr, privateKey)

	for {
		sshTries++
		if err != nil {
			if sshTries > 20 {
				return err
			}
			spinnerMsg.ShowMessage("SSH Connection Failed, Retrying... ")
			time.Sleep(time.Second * time.Duration(5))
			sshShellClient, err = GetSSHClient(ipAddr, privateKey)
			continue
		}
		break
	}
	sshTries = 0
	defer sshShellClient.Close()

	sshSftpClient, err := GetSSHClient(ipAddr, privateKey)
	for {
		sshTries++
		if err != nil {
			if sshTries > 20 {
				return err
			}
			spinnerMsg.ShowMessage("SFTP Setup Failed, Retrying... ")
			time.Sleep(time.Second * time.Duration(5))
			sshSftpClient, err = GetSSHClient(ipAddr, privateKey)
			continue
		}
		break
	}
	sshTries = 0
	defer sshSftpClient.Close()

	sftpClient, err := helpers.GetSFTPClient(sshSftpClient)
	for {
		sshTries++
		if err != nil {
			if sshTries > 20 {
				return err
			}
			spinnerMsg.ShowMessage("SFTP Setup Failed, Retrying... ")
			time.Sleep(time.Second * time.Duration(5))
			sftpClient, err = helpers.GetSFTPClient(sshSftpClient)
			continue

		}
		break
	}
	sshTries = 0
	defer sftpClient.Close()

	shell, err := GetSSHShell(sshShellClient)
	for {
		sshTries++
		if err != nil {
			if sshTries > 20 {
				return err
			}
			spinnerMsg.ShowMessage("SSH Shell Setup Failed, Retrying... ")
			time.Sleep(time.Second * time.Duration(5))
			shell, err = GetSSHShell(sshShellClient)
			continue
		}
		break
	}
	sshTries = 0

	tryExec := func(command string) (string, error) {
		tries := 0
		out, err := shell.Exec(command)
		for {
			tries++
			if err != nil {
				fmt.Println("Retrying Exec Error: ", err.Error())
				if tries > 20 {
					return "", err
				}
				time.Sleep(time.Second * time.Duration(2))
				out, err = shell.Exec(command)
				continue
			}
			break
		}
		return out, nil
	}

	spinnerMsg.ShowMessage("Updating Environment... ")
	_, err = tryExec("apt-get update -y -qq")
	if err != nil {
		return err
	}
	_, err = tryExec("apt-get upgrade -y -qq")
	if err != nil {
		return err
	}
	_, err = tryExec("apt-get install -y -qq git wget curl")
	if err != nil {
		return err
	}

	_ = spinnerMsg.StopMessage()
	fmt.Printf(" %s Updated Environment\n", checkMark)

	spinnerMsg.ShowMessage("Installing HAM Binary... ")
	if testingBin != "" {
		err = helpers.SFTPCopyFileToRemote(sftpClient, "/usr/bin/ham", testingBin)
	} else {
		_, err = tryExec(fmt.Sprintf("wget -O /usr/bin/ham \"%s\"", HAM_LINUX_BINARY_URL))
	}
	if err != nil {
		return err
	}
	_, err = tryExec("chmod a+x /usr/bin/ham")
	if err != nil {
		return err
	}

	_ = spinnerMsg.StopMessage()
	fmt.Printf(" %s Installed HAM to Remote Server\n", checkMark)

	// Copy HAM Configuration File
	spinnerMsg.ShowMessage("Copying Configuration... ")
	configFilePath, err := helpers.ConfigFilePath()
	if err != nil {
		return err
	}

	err = helpers.SFTPCopyFileToRemote(sftpClient, "/root/.ham.json", configFilePath)
	if err != nil {
		return err
	}

	fmt.Printf(" %s Copied Configuration to Remote Server\n", checkMark)

	spinnerMsg.ShowMessage("Making Required Directories... ")
	// Make required directories
	_, err = tryExec("mkdir -p /ham-build")
	if err != nil {
		return err
	}
	_, err = tryExec("mkdir -p /ham-recipe")
	if err != nil {
		return err
	}
	_, err = tryExec("mkdir -p /ham-files")
	if err != nil {
		return err
	}
	_, err = tryExec("mkdir -p /ham-output")
	if err != nil {
		return err
	}

	// Upload recipe repo (with SCP) or make the server download it.
	spinnerMsg.ShowMessage("Uploading Recipe to Remote Server... ")
	if usedGit {
		_, err = tryExec("rm -rf /ham-recipe")
		if err != nil {
			return err
		}
		if gitBranch != "" {
			_, err = tryExec(fmt.Sprintf("git clone --branch %s %s /ham-recipe", gitBranch, gitUrl))
			if err != nil {
				return err
			}
		} else {
			_, err = tryExec(fmt.Sprintf("git clone %s /ham-recipe", gitUrl))
			if err != nil {
				return err
			}
		}
	} else {
		// TODO: Make sure that it does not depend on trailing / for
		// local recipes
		_, err = tryExec("rm -rf /ham-recipe")
		if err != nil {
			return err
		}
		rootDir := dir

		walker := func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			destFile := strings.ReplaceAll(path, rootDir, "")

			if info.IsDir() {
				return sftpClient.MkdirAll(fmt.Sprintf("/ham-recipe/%s", destFile))
			}

			return helpers.SFTPCopyFileToRemote(sftpClient, fmt.Sprintf("/ham-recipe/%s", destFile), path)
		}

		err = filepath.Walk(rootDir, walker)
		if err != nil {
			return err
		}
	}

	spinnerMsg.ShowMessage("Uploading User Data... ")
	// Upload files from vars.json to server using SFTP securely.
	for srcFilePath, destFilePath := range fileUploads {
		try := 0
		for {
			try++
			err = helpers.SFTPCopyFileToRemote(sftpClient, destFilePath, srcFilePath)
			if err != nil {
				if try > 20 {
					return err
				}
				time.Sleep(time.Second * time.Duration(2))
				continue
			}
			break
		}
	}

	// Upload the vars.json file
	err = helpers.SFTPCopyFileToRemote(sftpClient, fmt.Sprintf("/ham-files/vars.json"), varsFilePath)
	if err != nil {
		return err
	}

	_ = spinnerMsg.StopMessage()

	if volumeLinuxDevice != "" {
		spinnerMsg.ShowMessage("Mounting Volume... ")

		mountStatus, err := tryExec("mountpoint /ham-build || true")
		if err != nil {
			return err
		}

		if strings.Contains(mountStatus, "not a mountpoint") {
			_, err = tryExec(fmt.Sprintf("mkfs.ext4 -F %s", volumeLinuxDevice))
			if err != nil {
				return errors.New("Volume Mkfs Failed")
			}

			_, err = tryExec(fmt.Sprintf("mount -o discard,defaults %s /ham-build", volumeLinuxDevice))

			if err != nil {
				return errors.New("Volume Mount Failed")
			}

		}

		_ = spinnerMsg.StopMessage()
	}

	_, err = tryExec("echo 'finished' > /tmp/ham.init.finished")
	if err != nil {
		return err
	}

	sftpClient.Close()
	sshSftpClient.Close()
	sshShellClient.Close()

	return nil
}

func askQuestions(hf *core.HAMFile, serverName string, answersJsonFilePath string, noconfirm bool) (string, map[string]string, error) {
	buildVars := core.NewVariables()
	optionalSuffix := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).SetString(" (OPTIONAL, Press ENTER to Skip)")
	varsFilePath := fmt.Sprintf("%s%c%s-vars.json", os.TempDir(), os.PathSeparator, serverName)
	varsJson := map[string]string{}
	fileUploads := map[string]string{}
	fileIndex := 0
	var err error

	if len(hf.Args) == 0 {
		err = helpers.DumpJsonFile(varsJson, varsFilePath)
		if err != nil {
			return varsFilePath, fileUploads, err
		}
		return varsFilePath, fileUploads, nil
	}

	banner.GetQuestionBanner()

	// Get Answers if Provided
	answers := map[string]string{}
	if len(answersJsonFilePath) != 0 {
		answers, err = helpers.ReadVarsJsonFile(answersJsonFilePath)
		if err != nil {
			return varsFilePath, fileUploads, err
		}
	}

	for _, arg := range hf.Args {
		placeholder := "Value"
		required := false
		valueType := core.VARIABLE_TYPE_VALUE

		argType := strings.ToLower(arg.Type)
		if argType == "file" {
			placeholder = "File Path"
			valueType = core.VARIABLE_TYPE_FILE_PATH
		} else if argType == "secret" {
			placeholder = "Secret"
			valueType = core.VARIABLE_TYPE_SECRET
		}

		if arg.Required != nil {
			required = *arg.Required
		}

		answerValue, answerOk := answers[arg.ID]
		if answerOk {
			buildVars.PutVar(arg.ID, answerValue, valueType)
			continue
		}

		if !required && noconfirm {
			continue
		}

		questionResponse := NewQuestionResponse(required, valueType == core.VARIABLE_TYPE_SECRET)

		suffix := ""
		if !required {
			suffix = fmt.Sprintf("%s", optionalSuffix)
		}

		runQuestionTeaProgram(questionResponse, arg.Prompt+suffix, placeholder)

		if questionResponse.err != nil {
			return varsFilePath, fileUploads, questionResponse.err
		}

		buildVars.PutVar(arg.ID, questionResponse.answer, valueType)
		fmt.Println()
	}

	// Build the vars.json file and get ready to upload
	// to the server once created
	for key, val := range buildVars.Vars {
		if val.Type == core.VARIABLE_TYPE_VALUE ||
			val.Type == core.VARIABLE_TYPE_SECRET {
			if len(val.Value) != 0 {
				varsJson[key] = val.Value
			}
		} else if val.Type == core.VARIABLE_TYPE_FILE_PATH {
			exists, err := helpers.FileExists(val.Value)
			if err != nil {
				return varsFilePath, fileUploads, errors.New("Error finding Variables File (" + err.Error() + ").")
			}

			if !exists {
				return varsFilePath, fileUploads, errors.New("File given in Variables does not Exists.")
			}

			fileIndex++
			varsJson[key] = fmt.Sprintf("/ham-files/%d", fileIndex)
			fileUploads[val.Value] = varsJson[key]
		}
	}
	err = helpers.DumpJsonFile(varsJson, varsFilePath)
	if err != nil {
		return varsFilePath, fileUploads, err
	}

	return varsFilePath, fileUploads, nil
}

func trackRemoteServerProgress(host string, sshPrivateKey string, tail chan string) (SSHShellCode, error) {
	sshClient, err := GetSSHClient(host, sshPrivateKey)
	if err != nil {
		return SSH_SHELL_CANNOT_GET_CLIENT, err
	}
	defer sshClient.Close()

	shell, err := GetSSHShell(sshClient)
	if err != nil {
		return SSH_SHELL_CANNOT_GET_SESSION, err
	}

	err = runProgressTeaProgram(shell, tail)
	if err != nil {
		return shell.code, err
	}

	return shell.code, nil

}
