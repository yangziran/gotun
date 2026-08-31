package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/yangziran/gotun/pkg/crypto"
	"github.com/yangziran/gotun/pkg/logger"
)

var (
	plaintext string
	secretKey string
)

var encryptCmd = &cobra.Command{
	Use:   "encrypt",
	Short: "加密密码以便安全地存储在 config.yaml 中",
	Long: `使用 AES-GCM 算法对明文密码进行标准加密。
输出的密文可以直接粘贴到 config.yaml 的 'password' 字段中，
并设置 'encrypted: true' 标识。
启动隧道时，请通过 GOTUN_SECRET_KEY 环境变量提供完全相同的解密密钥。`,
	Run: func(cmd *cobra.Command, args []string) {
		if plaintext == "" {
			logger.Error("必须提供要加密的明文密码 (-p)")
			os.Exit(1)
		}

		if secretKey == "" {
			secretKey = os.Getenv("GOTUN_SECRET_KEY")
			if secretKey == "" {
				secretKey = crypto.DefaultSecretKey
				logger.Warn("未提供自定义密钥(-k)，也未设置环境变量，将使用内置兜底密钥进行加密")
			}
		}

		ciphertext, err := crypto.Encrypt(plaintext, secretKey)
		if err != nil {
			logger.Error("加密失败", "err", err)
			os.Exit(1)
		}

		fmt.Println("==================================================")
		fmt.Printf("密文: %s\n", ciphertext)
		fmt.Println("==================================================")
		fmt.Println("请按照以下格式更新您的 config.yaml 对应的服务器节点：")
		fmt.Println("  password: \"<密文>\"")
		fmt.Println("  encrypted: true")
		fmt.Println("随后，如果您使用了自定义密钥，请携带密钥启动服务：")
		fmt.Println("GOTUN_SECRET_KEY=\"<您的密钥>\" ./gotun start -c config.yaml")
	},
}

func init() {
	rootCmd.AddCommand(encryptCmd)
	encryptCmd.Flags().StringVarP(&plaintext, "password", "p", "", "需要加密的明文密码")
	encryptCmd.Flags().StringVarP(&secretKey, "key", "k", "", "用于加密的私密密钥 (运行时需与 GOTUN_SECRET_KEY 保持一致)")
}
