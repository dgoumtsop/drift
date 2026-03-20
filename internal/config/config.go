package config
import "os"
type Config struct {
	Port        string
	BackendURL string
} 

func Load() (*Config, error){
	port:= getEnv("PORT", "8080")
	backendURL:= getEnv("BACKEND_URL", "https://httpbin.org")

	return &Config{
		Port:  port,
		BackendURL: backendURL,
	}, nil
}

func getEnv(key, defaultValue string) string{
	if value:= os.Getenv(key); value != "" {
		return value 
	}
	 return defaultValue
}
