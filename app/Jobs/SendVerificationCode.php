<?php

namespace App\Jobs;

use App\Models\NotificationChannel;
use App\Services\NotificationService;
use App\Services\TelegramBot;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Foundation\Queue\Queueable;
use Illuminate\Mail\Message;
use Illuminate\Support\Facades\Mail;
use Illuminate\Support\Str;
use Throwable;

/**
 * Sends a channel the one-time code that proves its target is read, so the
 * panel only alerts a target whose owner confirmed the code. A failed send
 * is kept on the channel and not retried; the owner can ask for a new code.
 */
class SendVerificationCode implements ShouldQueue
{
    use Queueable;

    public int $tries = 1;

    public function __construct(
        public int $channelId,
        public string $code,
    ) {}

    public function handle(TelegramBot $telegram): void
    {
        $channel = NotificationChannel::query()->find($this->channelId);

        if ($channel === null) {
            return;
        }

        $subject = __('Verify your RAYXO alerts');
        $body = __('Your verification code is :code. It expires in :minutes minutes. Ignore this message if you did not set up alerts.', [
            'code' => $this->code,
            'minutes' => NotificationService::CHALLENGE_MINUTES,
        ]);

        try {
            if ($channel->type === 'telegram') {
                $telegram->send($channel->target, $subject."\n\n".$body);
            } else {
                Mail::raw($body, fn (Message $message) => $message->to($channel->target)->subject($subject));
            }
        } catch (Throwable $exception) {
            $channel->forceFill(['last_error' => Str::limit($exception->getMessage(), 480)])->save();
        }
    }
}
