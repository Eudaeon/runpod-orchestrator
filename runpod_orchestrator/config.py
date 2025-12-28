import os
import sys
from pwnlogger import logger

# Authentication
CLERK_SESSION_ID = os.getenv("RUNPOD_SESSION_ID")
CLERK_COOKIE = os.getenv("RUNPOD_CLERK_COOKIE")

# API Endpoints
RUNPOD_GRAPHQL_URL = "https://api.runpod.io/graphql"
RUNPOD_HAPI_HOST = "https://hapi.runpod.net/v1/pod"

# Templates
SAGE_TEMPLATE_ID = "2agard1lia"
HASHCAT_TEMPLATE_ID = "6f211pvy7k"

# Infrastructure
VPS_IP = os.getenv("VPS_IP")
REVERSE_PORT = "8889"
LOCAL_SSH_PORT = "8888"


def verify_config():
    """
    Validates that all required environment variables are present.
    Exits the program if any critical configuration is missing.
    """
    required_vars = {
        "RUNPOD_SESSION_ID": CLERK_SESSION_ID,
        "RUNPOD_CLERK_COOKIE": CLERK_COOKIE,
        "VPS_IP": VPS_IP,
    }

    missing = [var for var, value in required_vars.items() if not value]

    if missing:
        logger.error("Missing environment variables:")
        for var in missing:
            logger.error(f"    - {var}")
        logger.error("Please ensure your environment variables are populated correctly")
        sys.exit(1)


verify_config()
