import json
import unittest

from extension.account import JsonValue
from extension.video import (
    VideoError,
    VideoPending,
    VideoReady,
    parse_prompt,
    parse_video_candidate,
)


class VideoContractTests(unittest.TestCase):
    def test_prompt_accepts_native_single_turn_and_rejects_unsupported_input(self):
        body = {"model": "gemini-web-omni", "contents": [{"role": "user", "parts": [{"text": "a ball"}]}]}
        self.assertEqual(parse_prompt(json.dumps(body).encode()), "a ball")
        for changed in (
            {**body, "tools": []},
            {**body, "contents": body["contents"] * 2},
            {**body, "contents": [{"role": "model", "parts": [{"text": "a ball"}]}]},
            {**body, "contents": [{"parts": [{"inlineData": {"mimeType": "image/png", "data": "AA=="}}]}]},
            {**body, "generationConfig": {"temperature": 1}},
        ):
            with self.subTest(body=changed), self.assertRaises(VideoError):
                parse_prompt(json.dumps(changed).encode())

    def test_video_chip_is_pending_not_successful_text(self):
        candidate: list[JsonValue] = [None] * 13
        candidate[1] = ["Generating your video.\nhttp://googleusercontent.com/video_gen_chip/0"]
        self.assertIsInstance(parse_video_candidate(candidate), VideoPending)

    def test_plain_text_or_missing_video_does_not_become_success(self):
        candidate: list[JsonValue] = [None] * 13
        candidate[1] = ["Cannot generate this video"]
        with self.assertRaises(VideoError):
            parse_video_candidate(candidate)

    def test_sparse_video_field_extracts_only_generated_attachment(self):
        asset: list[JsonValue] = [None] * 8
        asset[7] = ["https://example.invalid/preview.jpg", "https://example.invalid/generated.mp4"]
        info: JsonValue = [[[[asset]]]]
        candidate: list[JsonValue] = [None] * 13
        candidate[1] = ["Ready"]
        candidate[12] = [{"60": info}]
        result = parse_video_candidate(candidate)
        assert isinstance(result, VideoReady)
        self.assertEqual(result.url, "https://example.invalid/generated.mp4")


if __name__ == "__main__":
    unittest.main()
