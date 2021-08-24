from pathlib import Path
import logging
from m4b_merge import config
# Import for initial auth
import audible

# Get an instance of a logger
logger = logging.getLogger(__name__)


# registers the user
class AudibleAuth:
    auth_file = Path(config.config_path, ".aud_auth.txt")

    def __init__(self, USERNAME="", PASSWORD="", OTP=""):
        self.USERNAME = USERNAME
        self.PASSWORD = PASSWORD
        self.OTP = OTP
        self.CAPTCHA_URL = None
        self.CAPTCHA_GUESS = None

    def custom_otp_callback(self):
        return str(self.OTP).strip().lower()

    def custom_captcha_callback(self, captcha_url):
        logger.warning(
            "Open this URL in browser and then type your answer:"
        )
        print(captcha_url)

        self.CAPTCHA_GUESS = input("Captcha answer: ")
        return str(self.CAPTCHA_GUESS).strip().lower()

    def register(self):
        # Check if we're coming from web or not
        auth = audible.Authenticator.from_login(
            self.USERNAME,
            self.PASSWORD,
            otp_callback=self.custom_otp_callback,
            captcha_callback=self.custom_captcha_callback,
            locale="us",
            with_username=False,
            register=True
        )
        auth.to_file(self.auth_file)
