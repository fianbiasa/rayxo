<?php

namespace App\Enums;

enum NodeAppStatus: string
{
    case Pending = 'pending';
    case Active = 'active';
    case Failed = 'failed';
}
