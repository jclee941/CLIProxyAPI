from gemini_web2api import __main__ as upstream

from .server import WebHandler

upstream.GeminiHandler = WebHandler
upstream.main()
