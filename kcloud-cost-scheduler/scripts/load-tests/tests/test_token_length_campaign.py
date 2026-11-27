import unittest
from token_length_campaign import StreamState, integrate, p99


class MeasurementTests(unittest.TestCase):
    def test_role_empty_and_usage_not_first_content(self):
        s = StreamState()
        s.consume('{"choices":[{"delta":{"role":"assistant","content":""}}]}', 10)
        s.consume('{"choices":[{"delta":{"content":"hello"}}]}', 75)
        s.consume('{"choices":[{"delta":{"content":" world"},"finish_reason":"stop"}]}', 95)
        s.consume('{"choices":[],"usage":{"prompt_tokens":83,"completion_tokens":2}}', 100)
        s.consume('[DONE]', 105)
        self.assertEqual(s.first_sse_ms, 10)
        self.assertEqual(s.content_ms, [75, 95])
        self.assertEqual(''.join(s.text), 'hello world')
        self.assertTrue(s.done)

    def test_energy_exact_window_and_gap(self):
        v = integrate([(0, 10), (1, 20), (2, 30)], .5, 1.5, 5)
        self.assertAlmostEqual(v['gross_J'], 20)
        self.assertAlmostEqual(v['net_J'], 15)
        self.assertAlmostEqual(v['coverage'], 1)
        self.assertEqual(integrate([(0, 10), (10, 10)], 1, 9)['coverage'], 0)

    def test_percentile_matches_previous_rule(self):
        self.assertEqual(p99(list(range(600))), 594)


if __name__ == '__main__':
    unittest.main()
