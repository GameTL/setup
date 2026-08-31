curl -fsSL https://lmstudio.ai/install.sh | bash

echo "export PATH="/var/home/game/.lmstudio/bin:$PATH"" >> ./zshrc
zsh 

lms daemon up

